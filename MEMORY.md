# VIGIL MEMORY

**A runtime firewall that remembers agents across sessions, devices and restarts.**

Kill the terminal and VIGIL used to forget everything. The agent would try
`pip install reqeusts` again, and you would pay an LLM to work out — again —
that it is a typosquat of `requests`. This is the layer that stops that.

Trust lives in a SQLite file, not in a context window. Recall is a keyed
lookup, not history replayed into a prompt: **measured p95 = 0.007 ms** over
2,500 rows on a cold client (`services/sibyl-memory/bench_scale.py`).

One honest limit, found by running the benchmark rather than assuming: the
`sibyl-memory-client` free tier is **capped at 2 MB**, so the "500 sessions x
115k tokens" figure could not be tested at that size — the write phase raises
`CapExceededError` first. Latency is flat and index-backed, so it is very
likely to hold, but *likely is not measured* and this document will not claim
it until it is. See §5.

---

## 1. Where memory is load-bearing — exact file:line

Every line below was verified by `grep -rn "sibyl" pkg/ services/` and by
checking that the cited line contains what this table claims (116 total
matches; these are the load-bearing ones).

### Read path — memory decides the call

| Step | File:line | What happens |
|---|---|---|
| 1 | [`firewall/firewall.go:282`](pkg/query-service/vigil/firewall/firewall.go#L282) | `checkSibylTrust()` — stage 1b, before graph or model |
| 2 | [`firewall/sibyl_trust.go:62`](pkg/query-service/vigil/firewall/sibyl_trust.go#L62) | trust ladder: strike 1 PAUSE / 2 BLOCK+ban / 3 permanent |
| 3 | [`firewall/sibyl_trust.go:71`](pkg/query-service/vigil/firewall/sibyl_trust.go#L71) | `Sibyl.TrustScore(ctx, agentID)` |
| 4 | [`sibyl/client.go:197`](pkg/query-service/vigil/sibyl/client.go#L197) | HTTP `POST /recall` |
| 5 | [`services/sibyl-memory/app.py:170`](services/sibyl-memory/app.py#L170) | `get_entity()` → SQLite point lookup |

### Write path — memory learns

| Step | File:line | What happens |
|---|---|---|
| 1 | [`firewall/firewall.go:745`](pkg/query-service/vigil/firewall/firewall.go#L745) | COLD journal: every ALLOW/BLOCK/PAUSE |
| 2 | [`firewall/firewall.go:766`](pkg/query-service/vigil/firewall/firewall.go#L766) | `recordSibylViolation()` on any refusal |
| 3 | [`firewall/sibyl_trust.go:122`](pkg/query-service/vigil/firewall/sibyl_trust.go#L122) | trust −20, 24h tool ban at strike 2 |
| 4 | [`firewall/sibyl_trust.go:159`](pkg/query-service/vigil/firewall/sibyl_trust.go#L159) | `Sibyl.RememberAgent()` — WARM upsert |
| 5 | [`firewall/sibyl_trust.go:178`](pkg/query-service/vigil/firewall/sibyl_trust.go#L178) | `Sibyl.Archive()` below the trust floor |
| 6 | [`sibyl/client.go:220`](pkg/query-service/vigil/sibyl/client.go#L220) → [`app.py:157`](services/sibyl-memory/app.py#L157) | `POST /remember` → `set_entity()` |

### Supporting API

| Tier | Read | Write |
|---|---|---|
| HOT `state/` | [`client.go:256`](pkg/query-service/vigil/sibyl/client.go#L256) | [`client.go:248`](pkg/query-service/vigil/sibyl/client.go#L248) |
| WARM `entities/` | [`client.go:197`](pkg/query-service/vigil/sibyl/client.go#L197) | [`client.go:213`](pkg/query-service/vigil/sibyl/client.go#L213) |
| COLD `journal/` | [`client.go:300`](pkg/query-service/vigil/sibyl/client.go#L300) | [`client.go:282`](pkg/query-service/vigil/sibyl/client.go#L282) |
| REFERENCE | [`client.go:318`](pkg/query-service/vigil/sibyl/client.go#L318) | [`client.go:311`](pkg/query-service/vigil/sibyl/client.go#L311) |
| ARCHIVE | — | [`client.go:328`](pkg/query-service/vigil/sibyl/client.go#L328) |

**To delete the memory layer:** comment out `firewall.go` lines **277–332**
(marked `DELETION TEST: MEMORY READ PATH — BEGIN/END`) and line **766**.

The read path is a marked block rather than a single line, because line 282
declares variables the following stage consumes — commenting that one line
alone breaks the build instead of demonstrating anything.

**Verified by performing it.** With the block commented out the build is
green, the memory service is *still running and still holding
`trust_score: 10, banned: ['run_command']`*, and the identical call is
**ALLOWED**. A running-but-ignored memory service is stronger evidence than
a stopped one, which could be mistaken for a connectivity failure.

One asymmetry worth knowing: **code deletion is silent, runtime failure is
loud.** Commenting the block out produces `ALLOW` with no error, because the
logging code is what you deleted. A runtime outage (`VIGIL_SIBYL_DISABLED=1`,
or the service down) emits:

```
level=ERROR msg="vigil: trust_score unavailable — cross-session enforcement
is DISABLED, repeat offenders will not be blocked" session=s4
tool=run_command agent=gate-agent error="trust_score unavailable: sibyl
memory layer is required for progressive enforcement: ... connection refused"
    ✔ attempt 1/1  ALLOW  stage=default  trust=n/a
      ^ trust_unavailable: this verdict is UNENFORCED, not clean
```

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


### Memory actually grows across sessions

Measured on a clean database, `gate-agent` running the same blocked command:

| | WARM entities | COLD journal | on-disk |
|---|---|---|---|
| after session 1 | 2 | 3 | 762,016 B |
| after session 2 | 2 | **5** | **819,696 B** |

COLD grows with every decision. WARM stays flat because agent trust is an
upsert on `UNIQUE(tenant_id, category, name)` — one row per agent, rewritten
as the score moves, so recall is a point lookup with no history to reduce
over.

Note the main `.db` file reads 4,096 bytes at both checkpoints: SQLite runs
in WAL mode, so writes live in `memory.db-wal` until checkpoint. Measuring
the main file alone would understate the data by two orders of magnitude.

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

**Offline, with one measured caveat.** Verified by blocking every outbound
socket and then writing and reading: a normal operation under the 2 MB free
tier completes with **zero network calls**. Above that cap the client contacts
`api.sibyllabs.org/api/plugin/check-write` for cap enforcement unless the
account is activated. So "no network egress" is true for the free tier as
shipped, and stops being true past 2 MB — stated here rather than discovered
later.

Three SDK method names differ from the obvious guess, found by introspection:

| Expected | Actual |
|---|---|
| `remember()` | `set_entity()` |
| `recall()` | `get_entity()` |
| `archive()` | `archive_entity()` |

`MemoryClient.local()` does exist as documented.


### Base anchoring, proven against a real chain

```bash
./demo/anchor_proof.sh     # requires foundry; no funds, no mainnet
```

Runs a local EVM node on Base Sepolia's chain id (84532), deploys the real
`VigilAnchor.sol`, and drives it through VIGIL's own Go anchoring code:

```
[3] anchoring two ledger links through VIGIL's own Go code
    tx1 0xe15fbdd72145c17f84abc4551c380540998ea01222c1b1154954619a394b9c82
    tx2 0xea4cec2cb503c88793eb18c235aa2493115ef260c4321e34a39bbdd429d754c4
[4] verifying on chain
    receipt status  : 1   gas 69848
    anchorCount     : 2
    latestHash      : 0x2222…2222
    verifyHead real : true
    verifyHead fake : false
[5] tamper guard — anchoring a link that skips a decision
    reverted as expected (prevHash does not continue this chain)
    head unchanged after the rejected attempt
ANCHOR PROOF PASSED
```

This proves the calldata encoding, EIP-1559 signing, chain-id guard, gas
estimation and contract logic are correct — a real node accepted the
transactions and the contract's state changed accordingly. The tamper guard
is the security property: an operator who deletes a decision cannot anchor
the next one without breaking continuity on a ledger they do not control.

**It proves nothing about public Base.** Those hashes exist only on the local
chain the script starts, and the explorer URLs VIGIL derives from the chain
id will not resolve. Public anchoring still needs a funded signer, and
`/vigil/base/status` reports `anchoring_enabled: false` until it has one.

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
