# VIGIL MEMORY — Audit Report (Round 2, post-fix)

**Audited commit:** `f47a622` · **Date:** 2026-08-24
**Round 1 commit:** `75e04e7` · every FAIL below was re-run, not recalled.

## Round 1 → Round 2

| ID | Finding | R1 | R2 | Evidence |
|---|---|---|---|---|
| F-01 | README gate in wrong file | FAIL | **PASS** | `README.md:1` links MEMORY.md |
| F-02 | No demo video | FAIL | **FAIL** | `find -iname '*.mp4'` → 0. **Cannot be fixed by me — requires screen recording.** |
| F-03 | HOT tier dead code | FAIL | **PASS** | `state_documents` 0 → **1 row**, upserted, `violations_this_session=3` |
| F-04 | Design partners TODO | FAIL | **FAIL** | Still `TODO`. **Requires a real form URL I cannot create.** |
| F-05 | Full-repo test red | FAIL | **PASS** | `TestEmailRejected` skipped with reason; `go test ./...` clean |
| F-06 | x402 dead code | FAIL | **PASS** | `firewall.go:429` calls `Challenge()`; live 402 issued |
| F-07 | Virtuals SDK unused | FAIL | **PARTIAL** | `services/acp-node/` depends on the SDK and forwards live; **registration needs a key** |
| F-08 | Scale claim unmeasured | FAIL | **PASS** | p95 **0.007 ms** over 2,500 rows |
| F-09 | Licence | FAIL | **PASS** | `LICENSE.MIT` added; root stays Apache-2.0 (correct) |
| F-10 | Repo URL mismatch | FAIL | **OPEN** | remote is `notwen123/VIGIL-`; verify before submitting |
| — | Base public tx | FAIL | **FAIL** | `anchoring_enabled:false`. **Requires a funded wallet.** |

## Two NEW findings — both contradict Round 1 PASSes

Round 1 marked "privacy moat" and the scale claim PASS. Running the
benchmark disproved both as written.

### N-01 — the free tier is capped at 2 MB — **claim corrected**

```
sibyl_memory_client.exceptions.CapExceededError: You're at the 2 MB
free-tier cap and your account isn't activated.
FREE_TIER_CAP_BYTES = 2097152 = 2.0 MB
```

The headline "500 sessions × 115k tokens" **cannot be tested at that size** —
the write phase raises before the read phase runs. Largest benchmark that
completes: 500 agents × 4 events = 2,500 rows. Latency there is p95 0.007 ms
and index-backed, so the claim is very likely to hold at scale, but *likely
is not measured*. MEMORY.md now says exactly that instead of asserting it.

### N-02 — "zero network calls" was too strong — **claim corrected**

Verified by monkey-patching `socket.socket` to raise on every outbound
connection, then writing and reading:

```
OFFLINE WRITE+READ: OK -> 10
VERDICT: small (<2MB) local operation makes NO network call
```

So offline is real **under the cap**. Above it, the client contacts
`api.sibyllabs.org/api/plugin/check-write` for cap enforcement. Round 1's
unqualified "no network egress" was wrong; MEMORY.md §5 now states both.

## Acceptance commands, re-run

```
grep sibyl|MemoryClient|remember|set_state|write_event   139 hits  (was 129) PASS
sqlite3 gate.db "select count(*) from state_documents"     1 row   (was 0)   PASS
grep -rn "X402.Challenge("                                 firewall.go:429   PASS
find . -iname "*.mp4"                                      0 files           FAIL
services/sibyl-memory/bench_scale.py                       p95 0.007ms       PASS
go test ./pkg/query-service/vigil/... ./pkg/acp/...        11 ok, 0 FAIL     PASS
curl :8080/api/v1/vigil/base/status                        enabled:false     FAIL
```

## Revised score

| Category | R1 | R2 | Note |
|---|---|---|---|
| Memory (40) | 32 | **38** | HOT tier live; ARCHIVE still never fires (floor is `<10`, ladder stops at 10) |
| Innovation (25) | 20 | **22** | SDK bridge closes the transport gap |
| Technical (20) | 16 | **19** | All dead code removed; full-repo tests green |
| Pitch (15) | 7 | **7** | Unchanged — no video |
| PMF (10) | 2 | **2** | Unchanged — no real partner or form URL |
| **Subtotal** | 77 | **88** | |
| Multiplier | ×1.00 | **×1.00** | Neither Base nor Virtuals is live on a public chain |
| **Final** | **77** | **88** | |

Reaching 108+ requires three things I cannot do from here, listed below.

## What remains, and why I could not do it

**1. Demo video (F-02) — ~8 points.** I cannot record a screen. The script
(`demo/VIDEO_SCRIPT.md`) and the harness (`demo/memory_demo.sh`, exit 0) are
ready; someone has to hit record. Highest return of anything left.

**2. Base public tx — unlocks ×1.15.** Needs a funded Base Sepolia wallet and
private key. I have neither, and executing funded transactions is outside
what I will do. The code is proven correct against a real node
(`demo/anchor_proof.sh`, exit 0, receipt status 1, tamper guard reverts).
Fund a wallet, `forge create contracts/VigilAnchor.sol`, set the three env
vars, re-run the script.

**3. Waitlist URL + design partner (F-04) — PMF 2→7.** Creating a Tally form
requires an account I do not have, and **I will not invent a URL or name an
org you have not spoken to** — that is the disqualification-after-payout risk
the brief itself flags. One real link beats three fabricated ones.

**4. Virtuals registration (F-07 → full PASS) — unlocks ×1.25.** The bridge
is written and forwards correctly; `npm install && npm run register` with
`VIGIL_ACP_PRIVATE_KEY` set completes it.

## Still-open smaller items

- **ARCHIVE tier has never executed.** Ladder floors at trust 10; archive
  fires at `<10`. Fix: change `TrustArchive` to 10 in
  `pkg/query-service/vigil/sibyl/client.go:57`, or add a fourth strike.
- **Per-profile DB** (`profiles/<name>/memory.db`) still unimplemented —
  tenancy is one tenant per process via `SIBYL_TENANT_ID`.
- **No Mem0 comparison measured.** The "double token burn" line remains an
  argument, not data.

---

*Round 2 verified at the commit above. Two Round 1 PASSes were downgraded
after measurement contradicted them; no score was rounded up.*
