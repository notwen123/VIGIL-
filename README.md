<div align="center">

# VIGIL MEMORY

**A runtime firewall for autonomous AI agents that remembers which agents are banned — across sessions, devices and process restarts.**

[Live dashboard](https://vigil-sibyl-memory.vercel.app/) ·
[API](https://vigil-cuy2.onrender.com) ·
[Memory map with file:line](./MEMORY.md) ·
[Deep audit](./DEEP_INTEGRATION_REPORT.md) ·
[Waitlist](https://tally.so/r/XxPJAe)

![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square\&logo=go\&logoColor=white)
![Memory](https://img.shields.io/badge/Sibyl%20Memory-load--bearing-7C3AED?style=flat-square)
![Base](https://img.shields.io/badge/Base%20Sepolia-anchored-0052FF?style=flat-square)
![Tests](https://img.shields.io/badge/12%20packages-passing%20offline-success?style=flat-square)
![License](https://img.shields.io/badge/License-Apache--2.0%20%2B%20MIT-green?style=flat-square)

</div>

---

An AI agent tries to install `reqeusts` — a typosquat of `requests`. VIGIL
blocks it. Before, you would kill the terminal and the agent would try the
same thing an hour later, and you would pay a language model to work out,
again, that it is a typosquat.

Now it is remembered. **Kill every process, start a fresh session, and the
same agent is still blocked** — because its trust score is a row in a
database, not history replayed into a context window.

Recall is a keyed lookup, not a search: **~1 ms in-process**, ~1–2.6 ms
through the local HTTP memory service, ~260 ms on the hosted split
deployment. There are no vectors, no embeddings, and **no LLM call anywhere
on the enforcement path**.

## The proof, not the pitch

`./demo/memory_demo.sh` — 30 seconds, no API keys, no network. Verbatim
output:

```text
[1] SESSION 1 — agent attempts a typosquat, three times
    ✖ attempt 1/3  BLOCK  stage=code_graph     trust=50   model=none  recall=1.95ms
    ‖ attempt 2/3  PAUSE  stage=sibyl_memory   trust=30   model=none  recall=1.22ms
    ✖ attempt 3/3  BLOCK  stage=sibyl_memory   trust=10   model=none  recall=1.65ms

[2] KILL — the process ends. This is the part that normally erases everything.
    memory service pid 384021 terminated
    on disk: 4096 bytes at /tmp/vigil-memory-demo.db

[3] SESSION 2 — brand new processes, no shared state
    Note: the denylist is NOT set this time — only memory can block now.
    ✖ attempt 1/1  BLOCK  stage=sibyl_memory   trust=10   model=none  recall=2.60ms

[4] DELETION TEST — remove the memory layer, run the identical call
    ✔ attempt 1/1  ALLOW  stage=default        trust=n/a  model=none
      ^ trust_unavailable: this verdict is UNENFORCED, not clean

GATE PASSED
```

Four things in that output are the whole argument:

1. **Session 2 runs with the denylist unset.** Memory is the only thing left
   that can produce a BLOCK — and it does.
2. **`model=none` on every line.** No LLM was consulted to reach any verdict.
3. **The ladder walks down and survives a kill**: 50 → PAUSE at 30 → BLOCK at
   10, then still 10 in a brand-new process.
4. **Stage [4] flips to ALLOW.** Sever the memory layer and the identical call
   goes through. That is the deletion test, executed.

The script parses its own output and **exits non-zero** if session 2 fails to
block or the memory-less run fails to allow. It cannot pass by narrating.

---

## For judges

### The gate

| Requirement | Evidence | Status |
|---|---|---|
| **Memory on the critical path** | [`firewall.go:300`](pkg/query-service/vigil/firewall/firewall.go#L300) — consulted *before* blast radius (352), cost forecast (406), behaviour (456) and the model (509) | ✅ Guarded by [`TestDeletionGate_MemoryIsConsultedBeforeGraphAndModel`](pkg/query-service/vigil/firewall/sibyl_deletion_test.go#L195), which fails the build if the ordering regresses |
| **Cold-start recall, fresh session** | Stage [3] above — new PID, denylist unset, trust recalled in 2.60 ms | ✅ |
| **Deletion breaks the core function** | Comment out [`firewall.go:295-350`](pkg/query-service/vigil/firewall/firewall.go#L295) and `:830`. The tree still **compiles**, and session 2 flips BLOCK → ALLOW | ✅ Performed, not asserted |
| **Read/write map findable in 2 minutes** | [MEMORY.md §1](./MEMORY.md) — 5 read hops, 6 write hops, each a `file:line` | ✅ |

The deletion test compiles cleanly on purpose. A "deletion test" that breaks
the build proves only that a symbol is referenced — not that any behaviour
depended on it.

### The rubric

| Criterion | What backs it |
|---|---|
| **Memory is load-bearing** | Five tiers carrying live data in one deployment — HOT `1` / WARM `2` / COLD `35` / REFERENCE `3` / ARCHIVE `3`. Memory is stage 2 of 6, not a sidecar. Progressive enforcement survives restarts, and the archive tier retires an agent **without un-banning it**. |
| **Innovation & originality** | Trust-as-memory inverts the usual firewall order. A repeat offender costs one local database read instead of a graph traversal plus a paid LLM judgement — so the system gets *cheaper and faster* the more hostile agents it has seen. |
| **Technical execution** | 12 Go packages green offline with zero API keys. 60+ real commits. Live deployment. Three adversarial audits — including ones that **downgraded their own earlier passes** when measurement contradicted them. |
| **Pitch & presentation** | `demo/demo.mp4` |
| **PMF** | [Waitlist](https://tally.so/r/XxPJAe). Design-partner slots deliberately empty — see [What is not done](#what-is-not-done). |

---

## How it works

Memory sits second, on purpose:

```text
deterministic policy  →  SIBYL MEMORY  →  HydraDB graph  →  LLM judgement
   free, instant         ~1ms, free      375ms–1s, free    seconds, $$$
                              ▲
                    a repeat offender stops here,
             and the graph and the model never run at all
```

Because memory answers before the graph and before any model, the expensive
stages are never reached for an agent already known to be hostile. That
ordering is not a convention — a test fails the build if a future edit moves
the graph earlier.

### The five tiers

| Tier | Table | What VIGIL puts in it |
|---|---|---|
| **HOT** | `state_documents` | Live session state — budget remaining, declared intent, violation count |
| **WARM** | `entities` | Agent trust: score, total blocks, banned tools, last violation type |
| **COLD** | `journal_events` | Every decision — ALLOW, PAUSE and BLOCK alike |
| **REFERENCE** | `reference_documents` | Operator runbooks and incident-response denylists |
| **ARCHIVE** | `archived_entities` | Agents retired below the trust floor, kept auditable |

The archive tier deserves one sentence of detail, because it is where the
interesting bug was. Archiving moves an agent out of the active set, so
`/recall` falls back to the archive. Without that fallback, retiring an agent
would have **silently un-banned it** — the harshest rung of the ladder
becoming the one that cleared the record. Found, fixed, and
[written up](./DEEP_INTEGRATION_REPORT.md) rather than quietly patched.

### The enforcement ladder

```text
trust 50  ─ first violation ──▶  30   PAUSE, human review
trust 30  ─ second violation ─▶  10   BLOCK + 24h tool ban + archived
trust 10  ─ any retry ────────▶  10   BLOCK from memory alone, ~1ms, no model
```

Strike two is only distinguishable from strike one by reading the past.
Without memory, every violation is a first violation forever: endless
warnings, nobody ever stopped.

---

## Partner stacks — real work, independently verifiable

### Base — the audit ledger is anchored on chain

Every decision is SHA-256 hash-chained locally. A local chain makes tampering
detectable to anyone holding an untampered copy — but not to someone who only
ever sees the operator's copy. Anchoring closes that gap.

| | |
|---|---|
| Contract | [`0x03164E72ffEd3293350A9b66e40aF00E59645020`](https://sepolia.basescan.org/address/0x03164E72ffEd3293350A9b66e40aF00E59645020) |
| First anchor | [`0x8fb2c862…e7d3a1`](https://sepolia.basescan.org/tx/0x8fb2c862953d342ede8b2ab42e2701ff73e435859993769d480d160c8ee7d3a1) — status `1`, block `45910360` |
| Chain | Base **Sepolia** (84532) |

The anchored hash is the **real head of this repository's audit ledger** —
355 events at the time, not a synthetic value:

```text
LEDGER_EVENTS=355
HEAD=778c4282f2f3698dfe8a6ad58053ce0ae51eb5b666b59e0bdc2693939edf4b69
anchorCount          6
verifyHead(real)     true
verifyHead(tampered) false
```

The on-chain head equals the local ledger head byte for byte, and a tampered
claim is rejected — the property the mechanism exists for, demonstrated on a
ledger the operator does not control.

> Deploying this for real found three bugs a local test chain structurally
> could not: a nonce race across concurrent anchors; a `prevHash` that meant
> the ledger predecessor where the contract meant the previously *anchored*
> hash, which made **every anchor after the first revert**; and a
> time-of-check race on the head. All three are fixed. The local proof had
> passed throughout, because it anchored two deliberately adjacent hashes and
> never submitted two at once.

### Virtuals ACP — counterparty trust from the same memory

```bash
curl -sX POST https://vigil-cuy2.onrender.com/api/v1/vigil/acp/job \
  -H 'Content-Type: application/json' \
  -d '{"job_id":"1","buyer_agent_id":"trading-agent-alpha","requested_tool":"run_command"}'
```

```json
{
  "verdict": "BLOCK",
  "reason": "refused from VIGIL memory: trading-agent-alpha has trust 10 after
             2 recorded violation(s) — recalled in 2.29ms without an LLM call",
  "trust_score": 10, "prior_blocks": 2, "recalled": true
}
```

A marketplace counterparty is refused, and told *why*, because that history
outlived the sessions it happened in. The [Node
bridge](services/acp-node/index.js) is deliberately logic-free — decisions
stay in [`pkg/acp/service.go:289`](pkg/acp/service.go#L289), because two
implementations could disagree about who is banned, which is exactly the bug
a memory layer exists to prevent.

---

## Verify in 3 minutes

```bash
git clone https://github.com/notwen123/VIGIL-.git && cd VIGIL-

go test ./pkg/query-service/vigil/... ./pkg/acp/...   # 12 packages, offline, no keys
./demo/memory_demo.sh                                 # kill, restart, still blocked
python services/sibyl-memory/test_memory.py           # two OS processes, prints both PIDs
```

The two-process test prints both PIDs so you can confirm they differ — if
session 2 recalled from a warm cache, the test would prove nothing:

```text
RESULT: PASS
  distinct processes : 384595 -> 384596
  recall latency     : 1.128 ms
  decision changed   : ALLOW (no memory) -> BLOCK (with memory)
  LLM calls          : 0
  network calls      : 0
  vectors/embeddings : 0
```

### Live endpoints

| | |
|---|---|
| Dashboard | https://vigil-sibyl-memory.vercel.app/ |
| API | https://vigil-cuy2.onrender.com |
| Memory service (OpenAPI) | https://vigil-sibyl-memory.onrender.com/docs |
| Anchoring status | `/api/v1/vigil/base/status` |
| ACP status | `/api/v1/vigil/acp/status` |

---

## What is not done

Stated here rather than left for a reviewer to discover.

- **Base mainnet.** The signer holds 0 ETH there. Anchoring is Sepolia only,
  and testnet transactions carry no economic weight.
- **Virtuals on-chain registration.** The agent's ERC-4337 smart account has
  no code deployed on either chain, so `/api/v1/vigil/acp/status` reports
  `identity_configured: true, registered: false`. That flag is verified by
  asking the chain — not by reading an environment variable — and flips on its
  own once the account is deployed.
- **Design partners: zero.** Naming any would be fabricated evidence.
- **The 2 MB free-tier cap** on `sibyl-memory-client` means the headline
  "500 sessions" benchmark cannot run at that size. The measured numbers are
  at the size that fits, and say so.
- **The ARCHIVE tier was dead code** until an audit caught it, and the fix
  exposed a worse bug underneath. Both are fixed and documented.

Three independent audits are in this repository, including the parts that
failed: [DEEP_INTEGRATION_REPORT.md](./DEEP_INTEGRATION_REPORT.md) ·
[FINAL_AUDIT_DONE.md](./FINAL_AUDIT_DONE.md) ·
[AUDIT_REPORT.md](./AUDIT_REPORT.md)

---

## Prior work

VIGIL — the deterministic-first runtime firewall this builds on: intent
policy, cost forecasting, behavioural detectors, HydraDB graph context,
multi-vendor LLM failover and the hash-chained audit ledger. Documented in
[docs/PLATFORM.md](./docs/PLATFORM.md).

VIGIL MEMORY does not replace any of it. It changes the ordering — memory now
answers first — and adds the one thing the original could not do: remember.

The repository is a fork of [SigNoz](https://signoz.io/) for its telemetry
pipeline. Original VIGIL MEMORY code lives in `pkg/query-service/vigil/`,
`pkg/acp/` and `services/`.

## Licence

Original VIGIL MEMORY code is **MIT** — [LICENSE.MIT](./LICENSE.MIT). The
inherited SigNoz fork remains **Apache-2.0** — [LICENSE](./LICENSE). The root
licence is unchanged, because that code is not ours to relicense. Both are
OSI-approved.

---

<div align="center">

**[MEMORY.md](./MEMORY.md)** — every load-bearing line, with numbers.

</div>
