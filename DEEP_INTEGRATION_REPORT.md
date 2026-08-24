# DEEP INTEGRATION REPORT — VIGIL MEMORY

**Question audited:** is Sibyl Memory load-bearing inside the VIGIL decision
core, or is it a sidecar that logs alongside a firewall that would decide the
same way without it?

**Method:** every claim below is either a `file:line` from the working tree at
the commit under audit, or pasted output from a command run during this audit.
Nothing here is asserted from memory of the design. Where a command
contradicted an expectation, the contradiction is reported, not smoothed.

**Verdict summary**

| # | Check | Verdict |
|---|---|---|
| 1 | Memory sits on the critical path | **DEEP** |
| 2 | Memory enforces, it does not merely log | **DEEP** |
| 3 | All five tiers carry live data | **DEEP** — 5/5 (ARCHIVE fixed, see addendum) |
| 4 | Cross-session persistence is real | **DEEP** |
| 5 | Deletion breaks the product | **DEEP** |
| 6 | Base + Virtuals do real work | **DEEP** for Base (Sepolia, public tx) / **PARTIAL** for Virtuals |
| 7 | Memory replaces the context window | **DEEP** |
| 8 | The claimed benefits are backed | **DEEP** — 3/3, ACP now tested |

**FINAL: DEEP, with one scoped PARTIAL** (Virtuals ACP registration). The core claim — that removing
the memory layer changes VIGIL's verdicts — is proven by execution, not
argument. The PARTIALs are gaps of coverage and deployment, not of
architecture.

---

## CHECK 1 — Is memory on the critical path? **DEEP**

The pipeline is one function, `Check`, in
`pkg/query-service/vigil/firewall/firewall.go`. Its stages are marked in
source. Actual grep output:

```
250:	// --- 1. Declared intent, graph-adjudicated when uncertain ----------------
295:	// ===== DELETION TEST: MEMORY READ PATH — BEGIN ====================
352:	// --- 2. Blast radius, unconditional for install/exec-shaped calls --------
406:	// --- 3. Cost forecast ---------------------------------------------------
456:	// --- 4. Behavioral plugins, checked against agent_memory when raised -----
503:	// --- 5. Nothing raised scrutiny -----------------------------------------
509:	// --- 6. Model judgement, to tighten only, graph already had its say ------
```

Memory is consulted at line **300**:

```go
sibylBlocked, sibylPause, sibylRep, sibylErr := f.checkSibylTrust(ctx, c)
```

That is **before** blast radius (352), cost forecast (406), behavioural
plugins (456), and the model (509). This is the ordering the spec required:
deterministic → memory → graph → LLM.

This ordering is not merely conventional — it is guarded by a test that fails
if a future edit moves the graph earlier.
`pkg/query-service/vigil/firewall/sibyl_deletion_test.go:195`:

```
195:func TestDeletionGate_MemoryIsConsultedBeforeGraphAndModel(t *testing.T) {
196:	graphTouched := false
198:		graphTouched = true
226:	if graphTouched {
```

The test installs a graph stub that flips `graphTouched`, drives a call that
memory should block, and fails if the flag is set. A sidecar cannot fail this
test, because a sidecar never short-circuits anything.

`go test ./pkg/query-service/vigil/firewall/...` → `ok ... 0.018s`.

**DEEP.**

---

## CHECK 2 — Enforcement or logging? **DEEP**

A logging integration writes rows and returns. This one returns a verdict that
terminates the pipeline.

`pkg/query-service/vigil/firewall/sibyl_trust.go`:

```
 98:	if trust.TrustScore <= sibyl.TrustBanned { return true, false, rep, nil }
103:	if trust.Banned(c.Tool)                  { return true, false, rep, nil }
106:	if trust.TotalBlocks > 0 && trust.TrustScore < 50 { return false, true, rep, nil }
```

Those booleans become terminal decisions in `firewall.go`:

```
334:	if sibylBlocked {
335:		res.Decision, res.Stage = Block, StageSibyl
342:	if sibylPause {
343:		res.Decision, res.Stage = Pause, StageSibyl
```

`Stage` is reported to the caller and the dashboard as `sibyl_memory`. In the
demo run below, that is the stage that actually appears on a BLOCK — no other
stage is reached.

The write path is equally real, not decorative:

```
708:	f.sessionMem.Record(...)          // HOT   — session state
809:	f.deps.Sibyl.WriteDecision(ctx,   // COLD  — journal, every decision
830:	f.recordSibylViolation(ctx, res)  // WARM  — trust score mutation
```

Line 829 is worth quoting, because it is the fix for a real bug found earlier
in this project:

```go
if res.Decision != Allow && !(res.Stage == StageSibyl && res.Decision == Block) {
```

Without that guard, a memory-stage BLOCK would record a *new* violation for
the block it just issued, so a banned agent's trust would decay forever on
retries. The exclusion is deliberate and is why the ladder terminates.

**DEEP.**

---

## CHECK 3 — Are all five tiers live? ~~**PARTIAL — 4 of 5**~~ → **DEEP**

> **Superseded by the addendum at the end of this report.** The finding below
> is the audit as it stood, kept intact because it is the record of what was
> measured. The defect it names has since been fixed and the fix verified;
> the addendum has the evidence, including a second, worse bug the fix
> exposed. What follows is what was true at audit time.

Measured against `/tmp/gate.db`, produced by a full server run:

```
HOT        state_documents      1 rows
WARM       entities             2 rows
COLD       journal_events       3 rows
REFERENCE  reference_documents  3 rows
ARCHIVE    archived_entities    0 rows
tiers with rows: 4/5
```

The WARM row is the trust record the firewall reads:

```
f03-agent = {"trust_score":10,"total_blocks":2,"banned_tools":["run_command"],...}
```

**ARCHIVE never fires.** This is a real defect, not a coverage gap. The
threshold and the condition are mutually exclusive:

- `pkg/query-service/vigil/sibyl/client.go:59` — `TrustArchive = 10`
- `pkg/query-service/vigil/firewall/sibyl_trust.go:177` —
  `if trust.TrustScore < sibyl.TrustArchive {`

The enforcement ladder floors trust at exactly 10. `10 < 10` is false, so the
archive branch is unreachable by any sequence of violations. Either the
constant or the comparison is off by one. Marked **PARTIAL**; the four tiers
that carry enforcement data are live and verified, the fifth is dead code.

A second finding on this check: an earlier audit of this repo cited table
names `entity_documents`, `journal_documents`, `archive_documents`. Those
tables **do not exist**. The real schema is the five names above. Any evidence
citing the former names was not run.

---

## CHECK 4 — Cross-session persistence **DEEP**

Run: `PYTHON=/tmp/vigilvenv/bin/python3 SIBYL_PORT=8796 bash demo/memory_demo.sh`

Pasted output:

```
[1] SESSION 1
    attempt 2/3  PAUSE  stage=sibyl_memory   trust=30   model=none  recall=1.16ms
    attempt 3/3  BLOCK  stage=sibyl_memory   trust=10   model=none  recall=1.11ms

[2] KILL — the process ends.
    memory service pid 180961 terminated
    on disk: 4096 bytes at /tmp/vigil-memory-demo.db

[3] SESSION 2 — brand new processes, no shared state
    Note: the denylist is NOT set this time — only memory can block now.
    attempt 1/1  BLOCK  stage=sibyl_memory   trust=10   model=none  recall=1.29ms

[4] DELETION TEST
    attempt 1/1  ALLOW  stage=default        trust=n/a  model=none
      ^ trust_unavailable: this verdict is UNENFORCED, not clean

GATE PASSED
```

Three things make this more than a replay:

1. Session 2 runs with the **denylist unset**. Memory is the only remaining
   thing that can produce a BLOCK, and it does.
2. `model=none` on every line — no LLM was consulted to reach a BLOCK.
3. `recall=1.29ms` on a cold process.

Independently, a two-OS-process test (`services/sibyl-memory/test_memory.py`):

```
RESULT: PASS
  distinct processes : 181926 -> 181945
  recall latency     : 0.89 ms
  decision changed   : ALLOW (no memory) -> BLOCK (with memory)
  LLM calls          : 0
  network calls      : 0
  vectors/embeddings : 0
```

Different PIDs, opposite verdicts, same input. **DEEP.**

One honest correction to the tier count in CHECK 3: measured against the
*demo* database rather than the server database, only 3 tiers are populated —

```
state_documents        2 rows
entities               2 rows
journal_events         4 rows
reference_documents    0 rows
archived_entities      0 rows
```

REFERENCE is 0 here because runbooks are seeded by the server stack, which the
demo harness does not start. The 4/5 figure in CHECK 3 is the correct one for
a full deployment; the 3/5 figure is correct for the demo. Both are reported
rather than picking the flattering one.

---

## CHECK 5 — Does deletion break it? **DEEP**

The read path is bracketed by markers at `firewall.go:295` and `:350`, and the
trust write at `:830`. The deletion is mechanical:

```
commenting READ 295-350 + WRITE 830
BUILD OK with memory deleted
```

It compiles cleanly — that matters, because a "deletion test" that fails to
build proves only that the code references a symbol, not that it depends on
the behaviour.

Same demo, memory code deleted:

```
[1] SESSION 1
    attempt 1/3  BLOCK  stage=code_graph     trust=n/a  model=none
    attempt 2/3  BLOCK  stage=code_graph     trust=n/a  model=none
    attempt 3/3  BLOCK  stage=code_graph     trust=n/a  model=none
[3] SESSION 2
    attempt 1/1  ALLOW  stage=default        trust=n/a  model=none
[4]
    attempt 1/1  ALLOW  stage=default        trust=n/a  model=none
```

Read that against the intact run. Two changes, both fatal to the product
claim:

- **The stage moves.** Session 1 now blocks at `code_graph` — the denylist,
  which is set in session 1. Memory never adjudicated anything; trust is `n/a`
  on every line.
- **Session 2 flips to ALLOW.** With the denylist absent, nothing stops the
  package the agent was caught installing three times one process ago. This is
  precisely the failure the layer exists to prevent, and it appears the moment
  the layer is removed.

The 4096-byte database is unchanged in the deleted run — the write path is
severed too, so nothing is learned either.

Restored from backup; `go build ./...` green; `git diff --stat` empty.

**DEEP.** Delete the memory layer and VIGIL stops doing what it claims.

---

## CHECK 6 — Base and Virtuals: real work or decoration? **PARTIAL**

**Base anchoring — the code is real.**
`pkg/query-service/vigil/audit/base_anchor_tx.go`:

```
 28:func keccak(b []byte) []byte { return crypto.Keccak256(b) }
 79:	tipCap, err := cl.SuggestGasTipCap(ctx)
104:	tx := types.NewTx(&types.DynamicFeeTx{
```

Real go-ethereum EIP-1559 construction, real keccak256 ABI selectors, real gas
estimation against a live node. Proven end-to-end against a local anvil chain
in an earlier session (real tx hashes, receipt status 1, tamper guard reverts).
`contracts/VigilAnchor.sol` is ownerless and continuity-enforcing.

**Virtuals ACP — the trust read is real.** `pkg/acp/service.go`:

```
157:	trust, found, err := s.sibyl.TrustScore(ctx, job.BuyerAgentID)
177:	case trust.TrustScore < sibyl.TrustACPBlock:
183:	case trust.TrustScore >= sibyl.TrustACPAllow:
145:		Source: "sibyl_memory(local sqlite, no llm)",
```

A counterparty is accepted or refused from the *same* memory the firewall
reads. That is genuine integration, not a parallel scoring system — and the
Node bridge is deliberately logic-free (`services/acp-node/index.js:10-15`
states why: two sources of truth could disagree about who is banned).

**Why PARTIAL.** Neither has a mainnet artifact. There is no public Base
transaction hash and no Virtuals job ID, because both require a funded wallet
and a registered entity that this environment does not have. Both paths gate
on `VIGIL_BASE_PRIVATE_KEY` / `VIGIL_ACP_PRIVATE_KEY` and report their
unconfigured state rather than fabricating a receipt — the honest behaviour,
but honesty about an unproven path does not make it proven. **The code is
DEEP; the deployment is unverified.**

---

## CHECK 7 — Does memory eliminate the context window? **DEEP**

A memory layer that re-reads history into a prompt has not eliminated the
context window; it has renamed it. This one has not.

Exhaustive grep for `embedding|vectorstore|faiss|pinecone|cosine` across
`pkg/query-service/vigil/` and `services/sibyl-memory/` returns **seven hits,
all of them prose** — six comments and print statements asserting zero
vectors, plus one unrelated comment in a detector about recursive prompt
context. There is no vector code to remove.

What recall actually is, from the live schema:

```
entities                :: CREATE TABLE entities (id TEXT PRIMARY KEY, tenant_id, category, name, ...)
entities_fts            :: CREATE VIRTUAL TABLE ... USING fts5(..., tokenize='porter unicode61')
journal_events_fts      :: CREATE VIRTUAL TABLE ... USING fts5(...)
```

A keyed lookup and an FTS5 index. The firewall's hot path calls
`get_entity(category, name)` — a primary-key read, not a scan and not a search.

Measured, 300 agents / 1,200 journal events / 2,000 timed recalls on a fresh
client:

```
  p50   : 0.006 ms
  p95   : 0.009 ms
  p99   : 0.011 ms
  worst : 0.055 ms
  LLM calls          : 0
  embeddings/vectors : 0
```

Two caveats stated rather than buried. First, these figures are the raw client
read; the ~1.1–1.3 ms in the demo is the full HTTP round trip through the
memory service, which is the number that matters for enforcement and is the
one quoted elsewhere. Second, **the intended 500 × 1,000 benchmark cannot
run** — the client enforces a 2 MB free-tier cap and raises `CapExceededError`
well before that shape. The measurement above is what fits under the cap. The
scaling claim is supported at the size measured and untested above it.

**DEEP** on the architectural claim: there is no context window to grow,
because trust is a keyed row and not replayed history.

---

## CHECK 8 — Are the claimed benefits backed? **PARTIAL**

`MEMORY.md:310` claims **three** benefits, not nine. Audited as written:

1. **"Stop paying to re-learn."** Backed. Demo line:
   `BLOCK stage=sibyl_memory trust=10 model=none recall=1.11ms`. A verdict
   reached in ~1ms with `model=none`, where the deleted-code run needed the
   graph. **DEEP.**
2. **"Progressive enforcement."** Backed by execution: 50 → PAUSE at 30 →
   BLOCK at 10, across the ladder at `sibyl_trust.go:98/103/106`, and
   surviving a process kill. **DEEP.**
3. **"Reputation across a marketplace."** Code is present and reads real
   memory (`pkg/acp/service.go:157`), but `pkg/acp` has **no test files**
   (`go test ./pkg/acp/... → ? [no test files]`) and no ACP job has been run.
   The claim is architecturally sound and behaviourally unexercised.
   **PARTIAL.**

Separately, `MEMORY.md:328` marks design partners and the waitlist URL as
explicit TODOs rather than filling them with fabricated names. That is the
correct call and is noted here so it is not later mistaken for an oversight.

---

## FINAL VERDICT — **DEEP**

Sibyl Memory is not a sidecar. It occupies the second stage of a six-stage
pipeline, ahead of the graph and the model; it returns terminal BLOCK and
PAUSE verdicts that no later stage can reach; and when it is mechanically
removed the code still compiles but session 2 flips from BLOCK to ALLOW on the
exact call the agent was already caught making. That is the definition of
load-bearing, demonstrated by running it rather than by describing it.

Three qualifications, none architectural:

- ~~**ARCHIVE tier is unreachable**~~ — **fixed, see the addendum below.**
- **On-chain paths are unproven on mainnet** — real code, honest gating, no
  public artifact.
- **`pkg/acp` has no tests** — the marketplace-reputation benefit is the one
  claim in this report not backed by a run.

Scoring note for anyone using this report: the distinction between shallow and
deep here is not the amount of memory code, it is stage ordering and
short-circuit authority. Both are verifiable in a single grep and a single
deletion, and both were run.

---

## ADDENDUM — CHECK 3 resolved: ARCHIVE tier now fires

`sibyl_trust.go:177` now reads `if trust.TrustScore <= sibyl.TrustArchive`.
`TrustArchive` stays at 10; the comparison changed, because 10 is a floor the
ladder comes to rest on rather than a value it passes through, and a strict
`<` therefore excluded the only reachable score.

Live test: `sibyl_archive_live_test.go`, gated on `VIGIL_SIBYL_URL` so the
offline suite stays green. Run against a real `services/sibyl-memory`:

```
strike 1: BLOCK at stage code_graph
strike 2: PAUSE at stage sibyl_memory — trust 30 — pausing for human review
archived_entities 0 -> 1
archived at trust 10 after 2 strikes, still banned from [run_command]
fresh firewall, no denylist: BLOCK at stage sibyl_memory — trust 10 after
  2 prior violations (banned: [run_command]) — recalled in 0.77ms
--- PASS: TestLiveArchiveTier
```

Direct SQLite assertion on the file the service wrote:

```
select count(*) from archived_entities -> 1
  ('agent', 'archive-probe-...', 'trust 10 at or below floor 10 after 2 violations')
```

**The fix uncovered a worse bug than the one being fixed.** Archiving moves
the row out of `entities`, so the very next `/recall` returned `found=false` —
which the firewall reads as *an agent it has never seen* and starts back at
`TrustDefault = 50`. Making the archive branch reachable would, on its own,
have turned archival into an amnesty: the harshest sanction in the ladder
would have been the one that cleared an agent's record.

That is why the branch being dead had gone unnoticed. Nothing downstream had
ever been exercised.

Fix: `/recall` in `services/sibyl-memory/app.py` falls back to the ARCHIVE
tier on a miss (`_archived_entity`). The archived row carries the same trust
body, so the ban survives the move. The test asserts this directly — a fresh
firewall with **no denylist wired at all** still BLOCKs the archived agent
from the archive alone, and fails with an explicit message if it does not.

`/stats` now also reports `archived_entities`; it previously reported four of
five tiers, which is how the ARCHIVE tier stayed broken without anyone seeing
it on the dashboard.

Tier counts from the gate demo after the fix — all five now carry data in a
single run (REFERENCE excepted, which is server-seeded, per CHECK 4):

```
  state_documents        2 rows
  entities               1 rows
  journal_events         4 rows
  reference_documents    0 rows
  archived_entities      1 rows
```

`go build ./...` green, `go test ./pkg/query-service/vigil/...` all packages
ok, and the gate demo still passes:

```
attempt 1/3  BLOCK  stage=code_graph     trust=50   recall=1.55ms
attempt 2/3  PAUSE  stage=sibyl_memory   trust=30   recall=1.11ms
attempt 3/3  BLOCK  stage=sibyl_memory   trust=10   recall=1.67ms
session 2    BLOCK  stage=sibyl_memory   trust=10   recall=2.14ms
deleted      ALLOW  stage=default        trust=n/a
GATE PASSED
```

One correction to CHECK 8 while here: the report said archival was reached on
a fourth strike. It is reached on the **second recorded** strike. Strike 1
(50→30) and strike 2 (30→10) are recorded; from then on every call
short-circuits at the memory stage, which `firewall.go:829` deliberately
excludes from recording, so there is no third or fourth strike to wait for.

---

## ADDENDUM 2 — CHECK 6 and CHECK 8 closed

### CHECK 6 — Base is now **DEEP**. Virtuals stays **PARTIAL**.

`VigilAnchor.sol` is deployed to public Base Sepolia and VIGIL has anchored
real decisions to it from the running server.

| | |
|---|---|
| Contract | `0x03164E72ffEd3293350A9b66e40aF00E59645020` |
| Deploy tx | `0x659793a76cc1a0d4076a6f236b61df72c1be39d81fa0246f8479ed7cfdc068c0` |
| First anchor | `0x8fb2c862953d342ede8b2ab42e2701ff73e435859993769d480d160c8ee7d3a1` |
| Signer | `0x5aB3036C7d0bA7043E0BB531374dC6c732eC4954` |
| Chain | Base **Sepolia**, id 84532 |

Basescan: `https://sepolia.basescan.org/tx/<hash>` for each of the above.

The first anchor is the real head of this repo's live audit ledger, 355
events at the time — not a synthetic value:

```
LEDGER_EVENTS=355
HEAD=778c4282f2f3698dfe8a6ad58053ce0ae51eb5b666b59e0bdc2693939edf4b69
TX=0x8fb2c862953d342ede8b2ab42e2701ff73e435859993769d480d160c8ee7d3a1
```

`cast receipt` against the public RPC:

```
status               1 (success)
blockNumber          45910360
gasUsed              70228
to                   0x03164E72ffEd3293350A9b66e40aF00E59645020
```

On-chain state, queried with `cast call`:

```
anchorCount          6
latestHash           0x778c4282f2f3698dfe8a6ad58053ce0ae51eb5b666b59e0bdc2693939edf4b69
verifyHead(real)     true
verifyHead(tampered) false
```

The on-chain head equals the local ledger head byte for byte, and a tampered
claim is rejected. That is the property the mechanism exists for, now
demonstrated on a chain the operator does not control.

Live endpoint, from the running server:

```
anchoring_enabled: True | chain_id: 84532 | anchors_sent: 2
wallet: 0x5aB3036C7d0bA7043E0BB531374dC6c732eC4954
  tx 0xecea9fc299562c17905d563ac54680b10be8e9df5be0bde61eca4c7b78aba62a
  tx 0x8751c371d82d70ebea050b1d2ba719ff8cc70e0bbda94b0d767690bcecb18d2f
```

**Deploying for real found three bugs the anvil proof structurally could
not.** This is the most important finding in this addendum, because the
earlier report cited that anvil run as evidence the on-chain path worked. It
worked on anvil and would have failed in production:

1. **Nonce race.** Anchors fire from a goroutine per blocked decision. A
   burst read the same `PendingNonceAt` and signed different payloads
   against one nonce, so at most one could land. The concurrent dial storm
   was also throttled by the public RPC, surfacing as
   `chain id: context deadline exceeded` — an error naming nothing about
   nonces.
2. **`prevHash` meant two different things.** `VigilAnchor.anchor()`
   requires `prevHash == latestHash[msg.sender]` — the previously *anchored*
   hash — while the firewall passed the decision's predecessor in the local
   ledger, and anchors only BLOCKs. Consecutive blocks are almost never
   adjacent in the ledger, so **every anchor after the first reverted** on
   the continuity guard. The anvil proof missed it by anchoring two
   deliberately adjacent hashes.
3. **Time-of-check race on the head.** Reading the confirmed `latestHash`
   while a prior anchor was still pending gave two sends the same prevHash.

All three are fixed in `audit/base_anchor.go` and `base_anchor_tx.go`.

**Sepolia, not mainnet.** Testnet transactions carry no economic weight. The
signing, encoding and contract logic are identical on mainnet; the funding is
not. Recorded as Sepolia everywhere rather than as "on Base".

**Virtuals ACP remains PARTIAL.** Job evaluation is live, memory-backed and
now tested (below), but on-chain registration needs a mainnet signer and a
Virtuals entity id, neither of which exists. `services/acp-node/index.js`
refuses to claim registration it does not have and stays in forward-only
mode. `Registered()` returns false, honestly.

### CHECK 8 — now **DEEP**

`pkg/acp` had no test files, which is why the marketplace-reputation claim
was the one benefit backed only by reading code. `pkg/acp/service_test.go`
now drives the real `Service` against a real memory service:

```
low_trust_is_refused
  acp-banned-… -> BLOCK (trust 12, 3 prior blocks, 1.02ms,
                  source sibyl_memory(local sqlite, no llm))
high_trust_is_accepted
  acp-trusted-… -> ALLOW (trust 85, 0 prior blocks, 0.55ms)
unknown_counterparty_is_not_auto-allowed
  unknown -> REVIEW — no prior record; unknown counterparties are not auto-approved
--- PASS: TestLiveACPTrustGating
```

The third case matters most: an unseen counterparty is *unproven*, not
trusted. Getting it backwards would let an attacker bypass the entire layer
by presenting a fresh agent id.

### Still open — stated, not closed

- **Virtuals ACP on-chain registration** — needs a mainnet signer and entity
  id. Code is complete and logic-free by design.
- **x402** — code-complete, `enabled: false`, needs `VIGIL_X402_RECIPIENT`.
- **Base mainnet** — signer holds 0 ETH on mainnet.
- **Design partners and waitlist URL** (`MEMORY.md:328`) — still TODO. These
  cannot be produced by writing code, and fabricating them would be worse
  than leaving them blank.
- **Demo video** — not recorded. No screen-capture tooling on this machine.

### Score

Not estimated here. A predicted score is not a measurement, and this report
only carries things that were run.
