# VIGIL MEMORY — Audit Report

**Auditor stance:** adversarial. Every claim below was re-verified by running
it. Where the product fails, it is marked FAIL with the exact file to fix.
Nothing here is asserted from the build log or from memory.

**Audited commit:** `75e04e7` · **Date:** 2026-08-23
**Repo audited:** `github.com/notwen123/VIGIL-` — **not** `LSUDOKO/Vigil` (see F-01)

**Headline:** the gate passes. Three separate items are dead code or absent
that the brief explicitly required. Two submission blockers would cause
disqualification *after* payout if left as-is.

---

## SECTION 1 — GATE AUDIT (FAIL = DISQUALIFIED)

### 1.1 Load-bearing grep — **PASS**

```
$ grep -rn "sibyl\|MemoryClient\|remember\|set_state\|write_event" \
    pkg/ services/ --include="*.go" --include="*.py" | wc -l
129
```

Threshold was ≥5. Load-bearing sites:

| Direction | File:line |
|---|---|
| READ (enforce) | `pkg/query-service/vigil/firewall/firewall.go:282` |
| READ (ladder) | `pkg/query-service/vigil/firewall/sibyl_trust.go:62`, `:71` |
| READ (HTTP) | `pkg/query-service/vigil/sibyl/client.go:197` |
| READ (SQLite) | `services/sibyl-memory/app.py:170` |
| WRITE (journal) | `pkg/query-service/vigil/firewall/firewall.go:745` |
| WRITE (trust) | `pkg/query-service/vigil/firewall/firewall.go:766` → `sibyl_trust.go:122`, `:159` |
| WRITE (archive) | `pkg/query-service/vigil/firewall/sibyl_trust.go:178` |

### 1.2 Deletion test — **PASS**

Commented `firewall.go:277–332` (marked `DELETION TEST` block) and `:766`.
Build stayed green. Same agent, same command:

```
BEFORE: ✖ BLOCK  stage=sibyl_memory  trust=10   (memory present)
AFTER:  ✔ ALLOW  stage=default       trust=n/a  (memory code removed)
```

Memory service was **still running and still holding the record** during the
ALLOW — proving memory was the only thing blocking:

```
$ curl -X POST :8795/recall -d '{"category":"agent","name":"gate-agent"}'
  trust_score still stored: 10 | banned: ['run_command']
```

Runtime-failure path emits the required error:

```
level=ERROR msg="vigil: trust_score unavailable — cross-session enforcement
is DISABLED, repeat offenders will not be blocked" session=s4
tool=run_command agent=gate-agent error="...connection refused"
    ✔ attempt 1/1  ALLOW  stage=default  trust=n/a
      ^ trust_unavailable: this verdict is UNENFORCED, not clean
```

**Auditor note (not in the brief, but material):** *code deletion is silent,
runtime failure is loud*. Commenting the block out produces ALLOW with **no
error log**, because the logging code is what you deleted. Only the runtime
path logs. A judge who performs the deletion test by editing code will see
no error — brief them to use `VIGIL_SIBYL_DISABLED=1` if they want the log.

### 1.3 Cold-start recall — **PASS (script) / FAIL (video)**

Script exists and passes: `demo/memory_demo.sh`, exit 0, prints commit hash
and UTC timestamp at start. Cross-process proof also in
`services/sibyl-memory/test_memory.py` (two OS PIDs printed, 1.008 ms recall).

```
[1] SESSION 1  ✖ BLOCK trust=50 → ‖ PAUSE trust=30 → ✖ BLOCK trust=10
[2] KILL       memory service pid 504180 terminated
[3] SESSION 2  ✖ BLOCK stage=sibyl_memory trust=10 recall=1.61ms (denylist unset)
[4] DELETED    ✔ ALLOW — identical call
GATE PASSED
```

> **F-02 — no video file exists.** `find . -iname "*.mp4" -o -iname "*.mov"` → empty.
> Only `demo/VIDEO_SCRIPT.md`. **A script is not a submission.** FAIL until recorded.

### 1.4 README gate — **PASS**

`MEMORY.md` §1 "Where memory is load-bearing — exact file:line": two tables
(read path 5 hops, write path 6 hops), all links verified to resolve and to
contain what they claim. Findable in well under 2 minutes.

> **F-01 — the README gate lives in `MEMORY.md`, not `README.md`.** A judge
> told "check the README" opens `README.md` and finds the *old* VIGIL doc with
> no memory section. **Fix: add a link at the top of `README.md`.**

---

## SECTION 2 — 5-TIER DEEP INTEGRATION (40/40 check)

| Tier | Call site | Live rows | Verdict |
|---|---|---|---|
| **HOT** `state/` | `cost/session_memory.go:56` | **0** | **FAIL** |
| **WARM** `entities/` | `firewall/sibyl_trust.go:159` | 2 | PASS |
| **COLD** `journal/` | `firewall/firewall.go:745` | 6 | PASS |
| **REFERENCE** | `intent/runbook.go:102` (seeded `stack.go:194`) | 3 | PASS |
| **ARCHIVE** | `firewall/sibyl_trust.go:178` | 0 | PARTIAL |

### F-03 — HOT tier is DEAD CODE — **FAIL**

```
$ grep -rn "\.Record(ctx" --include="*.go" pkg/
  NEVER CALLED
$ sqlite3 gate.db "select count(*) from state_documents"
  0 rows          # after 2 full sessions and 6 decisions
```

`SessionMemory.Record()` (`pkg/query-service/vigil/cost/session_memory.go:52`)
is defined, documented, and **never invoked**. `NewSessionMemory` is never
constructed. The brief requires `set_state("session:{id}", …)` *rewritten
every tool call*. It is written **zero** times.

**Fix:** construct `cost.NewSessionMemory(mem, logger)` in
`appserver/stack.go` and call `.Record()` from `firewall.Commit()`
(`pkg/query-service/vigil/firewall/firewall.go:644`), which already runs after every executed call.

### ARCHIVE — PARTIAL

Code is correct and reachable (`sibyl_trust.go:178`, fires at `trust < 10`),
but 0 rows because the ladder floors at exactly 10 (`50→30→10`), and the
condition is `<10`. **The archive path has never actually executed.**
**Fix:** either a 4th strike, or change the floor to `<=10` in
`pkg/query-service/vigil/sibyl/client.go:57` (`TrustArchive`).

### Progressive enforcement — **PASS (not shallow)**

`firewall/sibyl_trust.go:95–112`:

```go
if trust.TrustScore <= sibyl.TrustBanned { return true, false, rep, nil }   // 3rd: permanent
if trust.Banned(c.Tool)                  { return true, false, rep, nil }   // 2nd: 24h ban
if trust.TotalBlocks > 0 && trust.TrustScore < 50 { return false, true, rep, nil } // 1st: PAUSE
```

24h ban written to WARM at `sibyl_trust.go:152–155`. Demonstrated live:
`50 → 30 → 10` with `banned_tools:['run_command']`, `banned_until` +24h.
**Not** a trivial `remember("user","john")`. Not shallow.

**Section 2 score: 32/40** — one of five tiers never executes, one never has.

---

## SECTION 3 — 9 BENEFITS

| # | Benefit | Verdict | Evidence |
|---|---|---|---|
| 1 | VIGIL updated by memory | **PASS** | `services/sibyl-memory/app.py` (FastAPI, 13 endpoints); `docker-compose.prod.yaml:45` service `vigil-sibyl-memory` on named volume `vigil_memory_data` |
| 2 | Market problem | **PASS** | `MEMORY.md:316` — "trading agents on Base, CI agents with package-install rights… agent forgets a risk limit after a restart". Not "chatbot remembers name" |
| 3 | Deep integration | **PARTIAL** | 4 of 5 tiers execute (see F-03) |
| 4 | Win every track | **FAIL** | See Section 4 |
| 5 | Save 80% LLM cost | **PASS** | Order enforced at `firewall.go:282` (memory) before the graph and model stages that follow it. Live: `BLOCK stage=sibyl_memory model=none recall=1.61ms`. Guarded by `TestDeletionGate_MemoryIsConsultedBeforeGraphAndModel` which fails the build if graph is touched first |
| 6 | PMF / funding | **FAIL** | `MEMORY.md:320–325` — all 4 partner/waitlist slots are literal `TODO` |
| 7 | Privacy moat | **PASS** | `app.py:74` `MemoryClient.local(path, tenant_id=…)`. `/health` reports `"vectors": false`, `"backend": "sqlite+fts5"`. No cloud call in the memory path |
| 8 | Runs offline | **PASS** | `env -i go test ./pkg/query-service/vigil/... ./pkg/acp/...` → 11 ok, 0 FAIL, no API keys |
| 9 | Production history | **PARTIAL** | 38 commits (not 2). But `go test ./...` at full repo scope **FAILS** |

### F-04 — design partners are TODO — **FAIL (disqualification risk)**

```
MEMORY.md:322  - [ ] Partner 1 — `TODO: real GitHub org / contact`
MEMORY.md:325  - [ ] Waitlist form — `TODO: real form URL`
```

Deliberately left blank rather than fabricated — fabricating them is the
stated disqualification-after-payout risk. But **blank scores zero on PMF**.
**Fix:** put a real Tally/Typeform URL in `MEMORY.md:325` and name three real
orgs you have actually spoken to. If you have none, ship the waitlist form
alone; one real link beats three fake ones.

### F-05 — full-repo `go test ./...` fails — **FAIL**

```
--- FAIL: TestEmailRejected (0.01s)
FAIL  github.com/SigNoz/signoz/pkg/alertmanager/alertmanagernotify/email
```

**Pre-existing upstream SigNoz failure**, not caused by memory work (no
`sibyl` reference in that package). But a judge running the literal command
sees a red FAIL. **Fix:** repair or skip it in
`pkg/alertmanager/alertmanagernotify/email/`, or document the exception.

---

## SECTION 4 — PARTNER MULTIPLIER (x1.25 check)

### Base — **FAIL for the multiplier**

**No public Basescan tx hash exists.**

```
$ curl :8080/api/v1/vigil/base/status
  anchoring_enabled: false      anchors_sent: 0      wallet: ""
$ startup log: base_anchoring=false
```

What *does* exist, verified against a real EVM node (`demo/anchor_proof.sh`,
exit 0):

```
[3] anchoring two ledger links through VIGIL's own Go code
    tx1 0xe15fbdd72145c17f84abc4551c380540998ea01222c1b1154954619a394b9c82
    tx2 0xea4cec2cb503c88793eb18c235aa2493115ef260c4321e34a39bbdd429d754c4
[4] receipt status: 1  gas 69848  anchorCount: 2
    verifyHead real: true   verifyHead fake: false
[5] tamper guard: reverted (prevHash does not continue this chain); head unchanged
```

This proves calldata encoding, EIP-1559 signing, chain-id guard and contract
logic (`audit/base_anchor.go`, `base_anchor_tx.go`, `contracts/VigilAnchor.sol`).
**It proves nothing about public Base** — those hashes exist only on the local
anvil chain and the derived Basescan URLs will not resolve.

**Fix:** fund a Base Sepolia wallet, `forge create contracts/VigilAnchor.sol`,
set `VIGIL_BASE_{RPC_URL,PRIVATE_KEY,CONTRACT}`. Code is ready; this is a
credential, not an engineering task.

### F-06 — x402 is DEAD CODE — **FAIL**

```
$ grep -rn "x402\|Challenge(" pkg/query-service/vigil/firewall/ .../mcp/
  RAIL NEVER TRIGGERED FROM FIREWALL/MCP
```

`cost.Rail` is constructed (`pkg/query-service/vigil/appserver/stack.go:217`) and surfaced on
`/vigil/base/status`, but `Challenge()` is **never called when a budget is
exhausted**. No 402 is ever issued. The brief requires "when HOT state
budget_left <0, trigger HTTP 402".

**Fix:** in `firewall/firewall.go:396` where `StateHardLimit` returns BLOCK,
call `rail.Challenge(sessionID, overspend)` and surface it through the MCP
adapter. Note the HOT tier it should read is itself dead (F-03) — fix F-03 first.

### Virtuals ACP — **PARTIAL**

| Requirement | Verdict |
|---|---|
| `pkg/acp/service.go` exists | PASS |
| Checks WARM trust_score | PASS — `pkg/acp/service.go:157` `s.sibyl.TrustScore` |
| ACP job ID logged | PASS |
| BLOCK when trust<30 | PASS |
| Uses `@virtuals-protocol/acp-node` | **FAIL** |
| Registered on chainId 8453 | **FAIL** |

Live evidence:

```
INFO vigil: ACP job decided from cross-session memory
  job_id=job-dkwi5sitz8hi buyer=gate-agent verdict=BLOCK trust=10 recall_ms=1.515
INFO vigil: ACP job decided ...
  job_id=acp-explicit-042 buyer=unknown-counterparty verdict=REVIEW trust=50
```

### F-07 — the Virtuals SDK is not used at all

```
$ grep -rn "@virtuals-protocol/acp-node" --include="*.json" --include="*.ts" .
  NOT USED ANYWHERE
```

The ACP service is a **Go reimplementation** of job evaluation, not the
official Node SDK. `Registered()` returns false; nothing is on chain 8453.
A judge checking "did they use the SDK" will find nothing.

**Fix:** add a thin Node bridge (`services/acp-node/`) that imports
`@virtuals-protocol/acp-node`, registers on 8453, and POSTs inbound jobs to
the existing `/vigil/acp/job`. The decision logic already works — this is
transport plus registration.

**Section 4 verdict: multiplier x1.00.** Neither Base nor Virtuals meets its
bar on public chain.

---

## SECTION 5 — MARKET NEXT LEVEL

| Claim | Verdict | Evidence |
|---|---|---|
| Eliminates context window | **PASS (design) / FAIL (proof)** | Trust is a keyed lookup, never pasted into context. Recall measured 1.0–1.9 ms across every run. **But no 500-session × 115k-token benchmark was ever run.** Largest measured DB: 6 journal rows |
| Cost: no embedding/vector fees | **PASS (claim) / FAIL (comparison)** | `/health` → `"vectors": false`. Zero embedding calls in code. **No measured Mem0 comparison exists** — the "double token burn" claim is unsubstantiated |
| Multi-tenant isolation | **PARTIAL** | Real `UNIQUE(tenant_id, category, name)` column, settable via `SIBYL_TENANT_ID` (`app.py:58`). **Per-profile DB at `<HERMES_HOME>/sibyl/profiles/<name>/memory.db` is NOT implemented** — one DB, one tenant per process |
| Defensibility | **PASS** | UNIQUE constraint verified in live schema; tamper-evident chain + on-chain continuity guard proven to revert on a gap |
| Competitor gap | **PASS (argument) / FAIL (data)** | Argument is sound and implemented — trust stored once, recalled by key. No benchmark against a history-passing framework |

### F-08 — headline scale claim is unmeasured

"500 sessions × 115k tokens, still sub-ms" appears in `MEMORY.md:9–11` and in
the video script. **It has never been run.** SQLite + a UNIQUE index makes it
extremely likely to hold, but likely is not measured.

**Fix:** write `services/sibyl-memory/bench_scale.py` — insert 500 agents ×
1,000 journal events, then time 1,000 random `get_entity` calls. That is a
30-line script and turns the strongest claim in the pitch from assertion into
evidence. **Highest evidence-per-effort item in this report.**

---

## SECTION 6 — SUBMISSION READINESS

| Item | Verdict | Evidence |
|---|---|---|
| Public repo | PASS | `github.com/notwen123/VIGIL-`, 38 commits, 2,116 files |
| **MIT license** | **FAIL** | `LICENSE` = **Apache 2.0** |
| Demo video 2–5 min | **FAIL** | No media file in repo (F-02) |
| README: what it does | PASS | `MEMORY.md` §1 |
| README: memory load-bearing file:line | PASS | `MEMORY.md` §1 (but see F-01) |
| README: partner stacks | PASS | `MEMORY.md` §6 with honest status per partner |
| README: how memory made it possible | PASS | `MEMORY.md` §7 |
| README: Prior Work = VIGIL | PASS | `MEMORY.md` §9 |
| README: deletion test proof | PASS | `MEMORY.md` §3 |
| Two public posts tagging @sibylcap | **FAIL** | No draft exists anywhere in repo |

### F-09 — license is Apache 2.0, not MIT

This is **correct and should not be "fixed" by relicensing.** VIGIL is a
SigNoz fork; SigNoz's Apache-2.0 code is not ours to relicense to MIT. Doing
so would be a licence violation.

**Fix:** if MIT is mandatory, dual-licence *only* the original code
(`pkg/query-service/vigil/`, `pkg/acp/`, `services/`) under MIT and state the
Apache-2.0 inheritance. Do not change the root `LICENSE`.

### F-10 — the repo URL does not match the brief

Brief says `github.com/LSUDOKO/Vigil`; actual remote is
`github.com/notwen123/VIGIL-`. Submitting the wrong URL is an
administrative disqualification. **Verify before submitting.**

---

## FINAL VERDICT

### Predicted score

| Category | Score | Reasoning |
|---|---|---|
| Memory (40) | **32** | Gate passes cleanly; HOT tier dead, ARCHIVE never fires |
| Innovation (25) | **20** | Enforcement ordering and the fail-open deletion test are genuinely novel. Loses on unused Virtuals SDK |
| Technical (20) | **16** | Builds, offline tests pass, live-verified. Loses on full-repo test failure and three dead-code paths |
| Pitch (15) | **7** | Written material is strong; **no video** is the single biggest loss |
| PMF (10) | **2** | Audience is real and specific; partners and waitlist are literal TODO |
| **Subtotal** | **77/110** | |
| Multiplier | **×1.00** | Neither Base nor Virtuals clears its bar on public chain |
| **Final Builder Score** | **77** | |

With the top 3 fixed: ~**94 × 1.15 = 108**.

### TOP 3 FAILS TO FIX FOR 1st PLACE

**1. Record the demo video (F-02).** Costs 15 minutes, worth ~8 points. The
script is written, the harness exits 0, and the 1:00–1:50 unbroken take is the
single most persuasive artefact you have. **Nothing else on this list has a
better return.** Currently a hard FAIL on an explicit gate item.

**2. Wire the HOT tier and x402 (F-03, F-06).** Two dead-code paths that both
appear in the brief. `state_documents: 0 rows` after two full sessions is
exactly what an auditor greps for, and it undercuts the "5-tier deep
integration" claim that carries 40 points. Fix: construct
`cost.NewSessionMemory` in `stack.go`, call `.Record()` from
`firewall.Commit()`, then trigger `rail.Challenge()` at the hard-limit branch.

**3. Get one real Base Sepolia tx + one real waitlist URL (F-04, plus Base).**
The anchoring code is proven correct against a real node — it needs a funded
testnet key, not engineering. A single public Basescan link converts ×1.00 to
×1.15. A single real waitlist URL converts PMF 2/10 to ~7/10. Both are
credentials/links, not code.

### Market readiness

**Not yet a startup. Past hackathon toy.**

Real: the enforcement ordering is a genuine architectural insight (cheapest
authoritative source first), the deletion test is stronger evidence than most
funded companies produce for their core claim, and local-first SQLite is a
defensible cost and privacy position.

Missing for a raise:
- **One measured benchmark.** Every scale and cost claim is currently
  asserted. `bench_scale.py` (F-08) fixes this in under an hour.
- **One design partner.** Not three — one real user who lost money to agent
  amnesia and will say so on the record.
- **Multi-tenancy beyond a column.** `SIBYL_TENANT_ID` is one tenant per
  process. Per-profile DB paths are unimplemented; that is the difference
  between a tool and a service.

**Verdict: strong hackathon submission with two unforced FAILs (video,
dead code) that cost more points than they take hours to fix.**

---

*Every FAIL names the file to change. No score above was rounded up, and no
evidence was reproduced from memory — each command in this report was run
against commit `75e04e7`.*
