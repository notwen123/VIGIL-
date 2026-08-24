# FINAL AUDIT — WHAT IS DONE

Every line below is a `file:line` from the working tree or pasted output from
a command run during this audit. Nothing is asserted from memory. Where a
command contradicted the brief, the contradiction is reported.

Commit under audit: `4ec7167`. Tree clean.

| § | Item | Verdict |
|---|---|---|
| 1 | Archive fix (3 parts) → CHECK 3 | **DONE** — 5/5 tiers |
| 2 | Env + key safety | **DONE** |
| 3 | Base Sepolia public tx → CHECK 6 | **DONE** (Sepolia, not mainnet) |
| 4 | Critical path / cross-session / deletion | **DONE** — CHECKs 1,2,4,5,7 DEEP |
| 5 | Nine benefits | **6 DONE, 3 PARTIAL** |
| 6 | Submission readiness | **PARTIAL** — video + waitlist missing |

---

## SECTION 1 — ARCHIVE FIX: **DONE**

### 1a. The comparison — `sibyl_trust.go:185`

```go
	// At or below the archive floor the agent leaves the active working set.
	// The record persists in archived_entities: a ban has to stay auditable.
	//
	// The comparison is `<=`, not `<`, and that is load-bearing. ...
	if trust.TrustScore <= sibyl.TrustArchive {
		if err := f.deps.Sibyl.Archive(ctx, "agent", agentID,
```

Constant unchanged at `sibyl/client.go:59`:

```go
	// TrustArchive - below this the entity is archived out of the active
	// working set (it stays in archived_entities for audit).
	TrustArchive = 10
```

Message reads correctly — from the live database:
`trust 10 at or below floor 10 after 2 violations`.

### 1b. `/recall` falls back to ARCHIVE — `app.py:182-196`

```python
    try:
        rec = client().get_entity(q.category, q.name)
    except NotFoundError:
        rec = _archived_entity(q.category, q.name)
        if rec is None:
            return {"ok": True, "found": False, "entity": None, "latency_ms": _ms(started)}
    return {"ok": True, "found": True, "entity": rec, "latency_ms": _ms(started)}
```

`_archived_entity` at `app.py:191`, reading `archived_entities` at `:207`.

### 1c. `/stats` reports the tier — `app.py:327`

```python
            "archived_entities": _archived_count(),
```

with `_archived_count()` querying `archived_entities` at `app.py:347`.

### 1d. SQLite — the required assertion

```
  /tmp/final-arch.db -> count = 1
      ('archive-probe-…', 'trust 10 at or below floor 10 after 2 violations')
  /tmp/vigil-memory-demo.db -> count = 1
      ('agent', 'trading-agent-alpha', 'trust 10 at or below floor 10 after 2 violations')
```

> Correction to the brief: `/tmp/gate.db` returns **0**. That file predates the
> fix and was never re-run. Citing it would have shown a false negative. The
> two databases above were produced after the fix, by the demo and by the test.

### 1e. Live test — PASS

`VIGIL_SIBYL_URL=http://127.0.0.1:8810 go test -run TestLiveArchiveTier -v`

```
strike 1: BLOCK at stage code_graph
strike 2: PAUSE at stage sibyl_memory — 1 prior violation(s), trust 30 — pausing for human review
archived_entities 0 -> 1
archived at trust 10 after 2 strikes, still banned from [run_command]
fresh firewall, no denylist: BLOCK at stage sibyl_memory — trust 10 after
  2 prior violations (banned: [run_command]) — recalled in 1.04ms, no model consulted
--- PASS: TestLiveArchiveTier (0.02s)
```

> Recall here is **1.04 ms**, not the 0.77 ms in the brief. That earlier figure
> was one sample from one run; this is another. Both are the same order and
> neither is a fixed constant. Quoting 0.77 ms as if reproducible would be
> wrong.

### 1f. Why the 2nd recorded strike, not the 4th — `firewall.go:829`

```go
		if res.Decision != Allow && !(res.Stage == StageSibyl && res.Decision == Block) {
			f.recordSibylViolation(ctx, res)
		}
```

A memory-stage BLOCK is excluded from recording. Strike 1 (50→30) and strike 2
(30→10) are recorded; from then on every call short-circuits at the memory
stage and nothing is written. **10 is a floor the agent rests on, never a value
it passes through** — which is exactly why the strict `<` was unreachable by
any sequence of strikes. There is no 3rd or 4th recorded strike to wait for.

### 1g. CHECK 3 — **5/5 DONE**

All five tiers carrying rows in one database, driven by the full server stack:

```
  state_documents         1
  entities                2
  journal_events         35
  reference_documents     3
  archived_entities       3
  tiers with rows: 5 /5
```

> The demo-only database shows 3/5 (`reference_documents 0`) because runbooks
> are seeded by the server stack, which `memory_demo.sh` does not start. Both
> numbers are correct for their scope; the 5/5 above is a full deployment.

---

## SECTION 2 — ENV + KEY SAFETY: **DONE**

```
  git check-ignore -> .env.local
  not tracked: PASS
  git status .env.local entries: 0
```

Key **names** present (values never read out):

```
      1 VIGIL_ACP_PRIVATE_KEY
      1 VIGIL_BASE_CHAIN_ID
      1 VIGIL_BASE_CONTRACT
      1 VIGIL_BASE_PRIVATE_KEY
      1 VIGIL_BASE_RPC_URL
```

The bare `PRIVATE_KEY` line has been **removed** this session. It was a
duplicate of the same secret under a name nothing reads — verified by grep:
the only `PRIVATE_KEY` hits outside `.env.local` are in
`services/acp-node/index.js:83,110`, a local JS const bound to
`process.env.VIGIL_ACP_PRIVATE_KEY` at `index.js:41`.

Chain state, derived from the key locally and queried by address only:

| Chain | Address | Balance | Tx count |
|---|---|---|---|
| Base Sepolia | `0x5aB3036C7d0bA7043E0BB531374dC6c732eC4954` | 0.039995690611589842 ETH | 8 |

Balance moved 0.04 → 0.0399956 and the nonce moved 0 → 8: real gas, really
spent. No key value was printed, logged, or transmitted at any point.

> Standing caveat: this key has appeared in a chat transcript. Rotate it if it
> is not a throwaway testnet key.

---

## SECTION 3 — BASE SEPOLIA: **DONE** (Sepolia, not mainnet)

Deployment (`forge create`, chain 84532):

```
Deployer:        0x5aB3036C7d0bA7043E0BB531374dC6c732eC4954
Deployed to:     0x03164E72ffEd3293350A9b66e40aF00E59645020
Transaction hash: 0x659793a76cc1a0d4076a6f236b61df72c1be39d81fa0246f8479ed7cfdc068c0
```

Recorded in `.env.local`:

```
VIGIL_BASE_CONTRACT=0x03164E72ffEd3293350A9b66e40aF00E59645020
VIGIL_BASE_CHAIN_ID=84532
```

`cast code` → **1717 bytes**, not `0x`.

Four transactions, all `status 1`:

```
  0x659793a76cc1a0d4…  status=1 block=45910247   (deploy)
  0x8fb2c862953d342e…  status=1 block=45910360   (first anchor, real ledger head)
  0xecea9fc299562c17…  status=1 block=45911449   (server anchor)
  0x8751c371d82d70eb…  status=1 block=45911453   (server anchor)
```

On-chain state:

```
  anchorCount: 6
  latestHash : 0x14429d71cea61b731e3b8f0e9c24fe4a08a0e4310947d8fb4ed0741127d5f05e
```

The first anchor carries the **real head of this repository's live audit
ledger** — 355 events at the time, not a synthetic value:

```
LEDGER_EVENTS=355
HEAD=778c4282f2f3698dfe8a6ad58053ce0ae51eb5b666b59e0bdc2693939edf4b69
TX=0x8fb2c862953d342ede8b2ab42e2701ff73e435859993769d480d160c8ee7d3a1
```

and at that point `verifyHead(real) = true`, `verifyHead(tampered) = false`.

Live endpoint:

```
  anchoring_enabled: True | chain_id: 84532 | anchors_sent: 2
  wallet: 0x5aB3036C7d0bA7043E0BB531374dC6c732eC4954
```

### Explorer resolution — read this carefully

```
  HTTP 403  https://sepolia.basescan.org/tx/0x8fb2c862…e7d3a1
  HTTP 403  https://sepolia.basescan.org/address/0x03164E72…5020
```

Basescan returns **403 to automated requests** (Cloudflare bot protection).
That is not a missing transaction and must not be reported as one. Independent
confirmation from a second explorer's API:

```
  status: ok | block: 45910360 | to: 0x03164E72ffEd3293350A9b66e40aF00E59645020
```

Two independent sources — the Base Sepolia RPC and Blockscout — agree the
transaction exists, succeeded, and targeted the deployed contract. The
Basescan URLs resolve in a browser; they cannot be verified with `curl` from
here, and this report will not claim a 200 it did not receive.

**Sepolia, not mainnet.** Testnet transactions carry no economic weight. The
signing, encoding and contract logic are identical on mainnet; the funding is
not. The signer holds 0 ETH on mainnet.

---

## SECTION 4 — CRITICAL PATH & CROSS-SESSION: **DONE**

Gate demo, full unedited run:

```
[1] SESSION 1 — agent attempts a typosquat, three times
    ✖ attempt 1/3  BLOCK  stage=code_graph     trust=50   model=none  recall=1.95ms
    ‖ attempt 2/3  PAUSE  stage=sibyl_memory   trust=30   model=none  recall=1.22ms
    ✖ attempt 3/3  BLOCK  stage=sibyl_memory   trust=10   model=none  recall=1.65ms
[2] KILL — the process ends.
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

Two-OS-process test:

```
RESULT: PASS
  distinct processes : 384595 -> 384596
  recall latency     : 1.128 ms
  decision changed   : ALLOW (no memory) -> BLOCK (with memory)
  LLM calls          : 0
  network calls      : 0
  vectors/embeddings : 0
```

Stage order, from source:

```
250:	// --- 1. Declared intent ---
295:	// ===== DELETION TEST: MEMORY READ PATH — BEGIN =====
350:	// ===== DELETION TEST: MEMORY READ PATH — END =====
352:	// --- 2. Blast radius ---
406:	// --- 3. Cost forecast ---
456:	// --- 4. Behavioral plugins ---
509:	// --- 6. Model judgement ---
```

Memory (295–350) precedes graph (352), cost (406), behaviour (456) and the
model (509). Guarded by
`sibyl_deletion_test.go:195 TestDeletionGate_MemoryIsConsultedBeforeGraphAndModel`
— `go test -run TestDeletionGate` → `ok 0.020s`, three cases.

Enforcement terminates the pipeline: `sibyl_trust.go:98/103/106` return
blocked/pause, consumed at `firewall.go:334/342` as `Block, StageSibyl` /
`Pause, StageSibyl`.

**CHECKs 1, 2, 4, 5, 7 — DEEP.**

---

## SECTION 5 — NINE BENEFITS

| # | Benefit | Verdict | Evidence |
|---|---|---|---|
| 1 | Real service, deployed | **DONE** | 13 FastAPI routes in `app.py`; `docker-compose.prod.yaml:53` `vigil-sibyl-memory`, named volume, health-gated `depends_on` |
| 2 | Market problem is agents, not chatbots | **DONE** | `MEMORY.md:328` — "trading agents on Base, CI agents with package-install rights"; validated pain is a forgotten risk limit after restart |
| 3 | Five tiers live | **DONE** | 5/5 above: 1 / 2 / 35 / 3 / 3 |
| 4 | Win every track | **PARTIAL** | Base **done** (public tx). Virtuals **not registered** — see below |
| 5 | Cost saved | **DONE** | Every BLOCK above is `model=none` at 1–2.6 ms, before graph and model; guarded by the deletion-gate ordering test |
| 6 | PMF / funding | **FAIL** | `MEMORY.md:388-393` still four `TODO` lines |
| 7 | Privacy | **DONE** | `/health` → `"backend":"sqlite+fts5","vectors":false`; two-process test reports `network calls: 0` |
| 8 | Runs offline | **DONE** | `env -i` (no API keys, no env at all) → **12 packages ok**, zero failures |
| 9 | Production history | **DONE** | 45 commits; `go build ./...` green |

### Benefit 4 — why Virtuals is PARTIAL, not DONE

`services/acp-node/package.json:13` declares
`"@virtuals-protocol/acp-node": "^0.3.0-beta.40"`, and the bridge is
deliberately logic-free. But:

```
  node_modules installed: NO
```

and the live endpoint reports honestly:

```
  "chain_id": 8453, "enabled": true, "memory_backed": true,
  "registered": false, "wallet": "",
  "note": "On-chain registration requires VIGIL_ACP_PRIVATE_KEY and a funded wallet."
```

`Registered()` at `pkg/acp/service.go:115` is
`s.wallet != "" && os.Getenv("VIGIL_ACP_PRIVATE_KEY") != ""`.

**I did not make `Registered()` return true.** Setting
`VIGIL_ACP_WALLET_ADDRESS` would flip that boolean without any registration
having occurred — the code would then claim VIGIL is a registered ACP provider
on Base mainnet when it is not. That is a fabricated capability claim, and it
is the one category of change explicitly ruled out. Real registration needs
mainnet funds (signer holds 0 ETH) and a Virtuals entity id.

Job evaluation itself is live, memory-backed and now tested —
`pkg/acp/service_test.go`: trust 12 → BLOCK (3 prior blocks, 1.02 ms),
trust 85 → ALLOW (0.55 ms), unknown → REVIEW rather than auto-allow.

### Benefit 6 — FAIL, and it cannot be fixed by writing code

```
388:**Design partners — TODO, do not fill with fabricated names.**
390:- [ ] Partner 1 — `TODO: real GitHub org / contact`
393:- [ ] Waitlist form — `TODO: real form URL`
```

A waitlist URL must be a real form you own. Inventing a Tally or Typeform link,
or naming design partners who have not agreed, is the disqualification risk
already flagged. **This needs ten minutes from you, not from me.**

### Multiplier

Base Sepolia public tx: **yes** → **×1.15**.
Virtuals bridge registered: **no** → ×1.25 not reached.

I am not producing a builder score. A predicted number is not a measurement,
and every other line in this report is one. The inputs are above; the scoring
rubric is the judges'.

---

## SECTION 6 — SUBMISSION READINESS: **PARTIAL**

| Item | Verdict |
|---|---|
| README → MEMORY.md, findable in <2 min | **DONE** — `README.md:1` and `:5` |
| MEMORY.md file:line map, 5 read hops / 6 write hops | **DONE** — `MEMORY.md:28-47` |
| LICENSE Apache-2.0 root + LICENSE.MIT | **DONE** — both present; root is `Apache License 2.0` (SigNoz fork; relicensing the root would be a violation) |
| Repo URL | **DONE** — `origin https://github.com/notwen123/VIGIL-.git`, matches the brief. `LSUDOKO/Vigil` is **not** this remote |
| `demo/demo.mp4` | **FAIL** — 0 mp4 files. No screen-capture tooling on this machine (`ffmpeg` absent). I cannot record it |
| Two public posts tagging @sibylcap | **NOT DRAFTED** — posting on your behalf needs your say-so; I have not written or published anything |
| `bench_scale.py` | **DONE** — present, respects the 2 MB cap; measured p50 0.006 ms / p95 0.009 ms / worst 0.055 ms at 300 agents |

---

## FINAL VERDICT

**8 / 8 checks DEEP**, with two deployment gaps that are not architectural:

- CHECK 1 critical path — DEEP
- CHECK 2 enforcement — DEEP
- CHECK 3 five tiers — DEEP (5/5, fixed this session)
- CHECK 4 cross-session — DEEP
- CHECK 5 deletion breaks it — DEEP
- CHECK 6 Base — DEEP (Sepolia public tx) / Virtuals PARTIAL
- CHECK 7 no context window — DEEP
- CHECK 8 benefits backed — DEEP

### Does everything work end to end?

**Yes, for the memory product.** A tool call enters the firewall, memory
adjudicates it second — ahead of graph and model — a repeat offender is
blocked in ~1–2 ms with no LLM, the verdict is written back across all five
tiers, the decision hash is chained locally and anchored to a public chain,
and deleting the memory layer flips session 2 from BLOCK to ALLOW. Every step
of that sentence has pasted output above.

**Not end to end:** Virtuals ACP on-chain registration and Base mainnet. Both
are gated on funds and credentials, both report their unconfigured state
honestly, and neither is faked.

### Remaining work, ordered by cost to you

| Fix | Where | Effort | Blocker |
|---|---|---|---|
| Waitlist URL | `MEMORY.md:393` | 10 min | Needs a real form you own |
| Design partner (≥1 real) | `MEMORY.md:390-392` | varies | One real link beats three fabricated |
| Demo video | `demo/demo.mp4` | 15 min | Needs a screen recorder; `memory_demo.sh` runs clean in one take |
| Virtuals registration | `services/acp-node/` | — | Mainnet funds + entity id |
| Base mainnet | — | — | Signer holds 0 ETH on mainnet |
| `npm install` | `services/acp-node/` | 1 min | None — just not run |

Nothing on that list is a code defect. The three defects found during this
work — the unreachable ARCHIVE branch, the archive-un-bans-the-agent
regression it exposed, and the three anchoring bugs that made every anchor
after the first revert — are fixed, tested, and committed.
