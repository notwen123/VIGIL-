# VIGIL MEMORY

**A runtime firewall that remembers agents across sessions, devices and restarts.**

Kill the terminal and VIGIL used to forget everything. The agent would try
`pip install reqeusts` again, and you would pay an LLM to work out — again —
that it is a typosquat of `requests`. This is the layer that stops that.

Trust lives in a SQLite file, not in a context window. An agent can have 500
sessions of 115k tokens each; recalling that it is on strike three still
takes about a millisecond and costs nothing.

---

## 1. Where memory is load-bearing — exact file:line

A judge should not have to hunt. These six lines are the whole mechanism.

### Read path — memory decides

| What | File | Line |
|---|---|---|
| Firewall consults memory **before** graph or model | [`firewall/firewall.go`](pkg/query-service/vigil/firewall/firewall.go#L277) | **277** |
| The trust ladder itself (strike 1/2/3) | [`firewall/sibyl_trust.go`](pkg/query-service/vigil/firewall/sibyl_trust.go#L62) | **62** |
| HTTP recall of an agent's trust record | [`sibyl/client.go`](pkg/query-service/vigil/sibyl/client.go#L197) | **197** |
| Service endpoint backing it | [`services/sibyl-memory/app.py`](services/sibyl-memory/app.py#L170) | **170** |

### Write path — memory learns

| What | File | Line |
|---|---|---|
| COLD journal: every ALLOW/BLOCK/PAUSE | [`firewall/firewall.go`](pkg/query-service/vigil/firewall/firewall.go#L745) | **745** |
| WARM trust: violation walks the ladder down | [`firewall/firewall.go`](pkg/query-service/vigil/firewall/firewall.go#L766) | **766** |
| Trust arithmetic and 24h tool ban | [`firewall/sibyl_trust.go`](pkg/query-service/vigil/firewall/sibyl_trust.go#L122) | **122** |
| Persisting the record | [`sibyl/client.go`](pkg/query-service/vigil/sibyl/client.go#L220) | **220** |

**To delete the memory layer:** comment out `firewall.go` lines **277–332**
(marked `DELETION TEST: MEMORY READ PATH — BEGIN/END`) and line **766**.

The read path is a marked block rather than a single line, because line 277
declares variables the following stage consumes — commenting that one line
alone breaks the build instead of demonstrating anything. The markers exist
so the boundary is unambiguous. Verified: with the block commented out the
build is green, the memory service is still running and still holding
`trust=10` for the agent, and the call is **ALLOWED** anyway.

---

## 2. Enforcement order

```
deterministic policy  →  SIBYL MEMORY  →  HydraDB graph  →  Featherless LLM
     free, instant        ~1ms, free      375ms–1s, free      seconds, $$$
```

Memory sits second on purpose. A repeat offender is stopped by a local
SQLite lookup, and the graph query and the model call never happen at all.
That ordering is enforced by a test that fails the build if it regresses:
`TestDeletionGate_MemoryIsConsultedBeforeGraphAndModel`.

---

## 3. The deletion test

**Claim:** remove the memory layer and VIGIL stops doing what it claims.

### Run it (30 seconds, no keys, no network)

```bash
./demo/memory_demo.sh
```

Real output from a real run:

```
[1] SESSION 1 — agent attempts a typosquat, three times
    ✖ attempt 1/3  BLOCK  stage=code_graph     trust=50   model=none
      package reqeusts is on the compromised-package list
    ‖ attempt 2/3  PAUSE  stage=sibyl_memory   trust=30   model=none
    ✖ attempt 3/3  BLOCK  stage=sibyl_memory   trust=10   model=none
      blocked from cross-session memory: trust 10 after 2 prior violations
      (banned: [run_command]) — recalled in 1.61ms, no model consulted

[2] KILL — the process ends.
    memory service pid 504180 terminated

[3] SESSION 2 — brand new processes, denylist NOT set
    ✖ attempt 1/1  BLOCK  stage=sibyl_memory   trust=10   recall=1.61ms

[4] DELETION TEST — VIGIL_SIBYL_DISABLED=1
    ✔ attempt 1/1  ALLOW  stage=default        trust=n/a
      ^ trust_unavailable: this verdict is UNENFORCED, not clean

GATE PASSED
```

The script parses its own output and exits non-zero if session 2 does not
BLOCK and the memory-less run does not ALLOW.

### As unit tests

```bash
go test -run TestDeletionGate -v ./pkg/query-service/vigil/firewall/
```

Three cases in
[`sibyl_deletion_test.go`](pkg/query-service/vigil/firewall/sibyl_deletion_test.go):
blocks with memory, **gets through without it**, and never reaches the graph
when memory has already decided.

### Cross-process persistence, proven separately

```bash
python services/sibyl-memory/test_memory.py
```

Two real OS processes with a kill between them. Prints both PIDs so you can
confirm they differ — if session 2 recalled from a warm cache the test would
prove nothing.

```
[session 1] pid 461866 wrote trust_score=12
[  kill  ] session 1 exited. 237,568 bytes remain on disk.
[session 2] pid 461877 recalled in 1.008 ms → BLOCK
RESULT: PASS
```

### Why it fails *open*

Deleting memory lets the agent through rather than blocking everything.
Failing closed would be safer in production, but it would let an operator
conclude the firewall still worked — noisily, but working. Letting the repeat
offender walk is the only outcome that makes the loss unmistakable. Affected
decisions are flagged `trust_unavailable` so the dashboard shows them as
**unenforced, not clean**.

---

## 4. The five tiers, and what VIGIL puts in each

| Tier | Contents | Written at |
|---|---|---|
| **HOT** `state/` | budget left, intent, violations this session, last tool — upserted in place | [`cost/session_memory.go`](pkg/query-service/vigil/cost/session_memory.go) |
| **WARM** `entities/` | agent trust, tool risk, policy scope — `UNIQUE(tenant_id, category, name)` | [`firewall/sibyl_trust.go:122`](pkg/query-service/vigil/firewall/sibyl_trust.go#L122) |
| **COLD** `journal/` | every decision, append-only, with decision hash | [`firewall/firewall.go:739`](pkg/query-service/vigil/firewall/firewall.go#L739) |
| **REFERENCE** | declared runbooks; written only by operators, never by the runtime | [`intent/runbook.go`](pkg/query-service/vigil/intent/runbook.go) |
| **ARCHIVE** | agents below the trust floor, retained for audit | [`sibyl_trust.go`](pkg/query-service/vigil/firewall/sibyl_trust.go) |

Progressive enforcement, defined entirely in terms of previous sessions:

| Strike | Outcome | Trust |
|---|---|---|
| 1st violation | **PAUSE**, recorded | 50 → 30 |
| 2nd, same agent+tool | **BLOCK** + 24h tool ban written to WARM | 30 → 10 |
| 3rd | **BLOCK** in every future session, on every device sharing the DB | ≤ 20 → archived |

Retrying a tool you have already been paused on counts as a strike. A
well-behaved agent backs off; one that does not is demonstrating exactly the
behaviour worth banning.

---

## 5. No vectors. No embeddings. No API key.

`sibyl-memory-client 0.4.10` — local-first SQLite with FTS5 (`porter
unicode61`), one file on disk. Verified by introspecting the installed
package, not by reading its docs:

- schema has a real `UNIQUE (tenant_id, category, name)` constraint
- `entities_fts` is a genuine FTS5 virtual table
- cold open + read in a fresh process: **~1–2 ms**

This is *why* memory can sit ahead of HydraDB and Featherless. An embedding
round-trip cannot compete, and does not need to: agent trust is a keyed
lookup, not a similarity search.

Three SDK method names differ from the obvious guess, found by introspection:

| Expected | Actual |
|---|---|
| `remember()` | `set_entity()` |
| `recall()` | `get_entity()` |
| `archive()` | `archive_entity()` |

`MemoryClient.local()` does exist as documented.

---

## 6. Partner stacks

| Partner | Where | Status |
|---|---|---|
| **Sibyl Memory** | [`services/sibyl-memory/`](services/sibyl-memory/), [`vigil/sibyl/`](pkg/query-service/vigil/sibyl/) | **Live.** Load-bearing; deletion test above. |
| **Base** — audit anchoring | [`audit/base_anchor.go`](pkg/query-service/vigil/audit/base_anchor.go), [`contracts/VigilAnchor.sol`](contracts/VigilAnchor.sol) | Code-complete, **unexecuted** — no funded signer. |
| **Base** — x402 payments | [`cost/x402.go`](pkg/query-service/vigil/cost/x402.go), [`x402_verify.go`](pkg/query-service/vigil/cost/x402_verify.go) | Code-complete, **unexecuted**. Inbound only. |
| **Virtuals ACP** | [`pkg/acp/service.go`](pkg/acp/service.go) | Job evaluation live and memory-backed; on-chain registration pending a signer. |
| **HydraDB** | [`vigil/hydra/`](pkg/query-service/vigil/hydra/) | Live. Now a *fallback* behind memory. |
| **Featherless / NVIDIA / Gemini** | [`vigil/llm/chain.go`](pkg/query-service/vigil/llm/chain.go) | NVIDIA + Gemini verified live. Last resort only. |

**Nothing has been executed on-chain.** No Basescan transaction hash is
claimed anywhere in this repository, because no funded wallet exists yet.
Everything on-chain activates on `VIGIL_BASE_PRIVATE_KEY` and reports its
unconfigured state honestly rather than fabricating a receipt.

---

## 7. How memory made this possible

Three things VIGIL could not do before, each impossible without state that
outlives the process:

1. **Stop paying to re-learn.** A typosquat adjudicated once is remembered.
   The second encounter costs a 1ms SQLite read instead of a graph query
   plus an LLM judgement.
2. **Progressive enforcement.** Strike two is only distinguishable from
   strike one by reading the past. Without memory every violation is a first
   violation forever — endless warnings, nobody ever stopped.
3. **Reputation across a marketplace.** [`pkg/acp`](pkg/acp/service.go) can
   refuse a counterparty and say *why* — "trust 12, three prior violations,
   last a typosquat" — because that history survived the sessions it
   happened in.

---

## 8. Who this is for

Teams running autonomous agents that spend money — trading agents on Base,
CI agents with package-install rights, ops agents with shell access. The
validated pain: an agent forgets a risk limit after a restart and repeats a
mistake it was already corrected for.

**Design partners — TODO, do not fill with fabricated names.**

- [ ] Partner 1 — `TODO: real GitHub org / contact`
- [ ] Partner 2 — `TODO: real GitHub org / contact`
- [ ] Partner 3 — `TODO: real GitHub org / contact`
- [ ] Waitlist form — `TODO: real form URL`

These are intentionally blank. Naming design partners we do not have would
be the one claim in this repository that could not be verified by running
something.

---

## 9. Prior work

VIGIL — the deterministic-first runtime firewall this builds on: intent
policy, cost forecasting, behavioural detectors, HydraDB graph context,
multi-vendor LLM failover, and the SHA-256 hash-chained audit ledger. See
[`README.md`](README.md) and [`Vigil-Whitepaper.pdf`](Vigil-Whitepaper.pdf).

VIGIL MEMORY does not replace any of it. It changes the ordering — memory
now answers first — and adds the one thing the original could not do:
remember.

---

## 10. Running it

```bash
# memory service (no key, no network)
pip install -r services/sibyl-memory/requirements.txt
python services/sibyl-memory/app.py

# firewall
go test ./...                    # passes offline
go run ./cmd/vigil-server

# everything
docker compose -f docker-compose.prod.yaml up
```

Licensed Apache-2.0 (inherited from the SigNoz fork this builds on; that
code is not ours to relicense).
