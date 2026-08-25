# JUDGE READINESS — how this gets scored, and where we actually stand

Criteria below are quoted from **hack.sibyllabs.org** (`/` and `/rules`),
fetched 2026-08-25. Not inferred, not remembered.

---

## 1. The real rubric

**Final score = (rubric + PMF bonus) × multiplier**

| Criterion | Weight | What the rules say ranks highest |
|---|---|---|
| Memory is load-bearing | **40** | "Sophistication and centrality of memory use; **competitive recall and dynamic-storage patterns rank highest**" |
| Innovation & originality | **25** | "Novelty and genuine usefulness matter; **clever but unusable work scores low**" |
| Technical execution | **20** | "Robust, clean code that **survives repeated testing**" |
| Pitch & presentation | **15** | "2–5 minute narrative where **the load-bearing moment is evident**" |
| PMF bonus | **+10** | Named audience, waitlist, design partners, real usage or pilots. "Market-size slides or unsubstantiated claims earn zero" |

**Multiplier** (diminishing, capped ×1.25): first stack +15%, second +10%.
Sibyl Memory is mandatory and **never counts** as a multiplier.

Ceiling: 110 × 1.25 = **137.5**.

## 2. It is two stages, and stage one is pass/fail

> "The gate passes only by majority vote; **ties result in disqualification**."

The gate test: *"Does the project still work if Sibyl Memory calls are
removed? If yes, it fails."* Three required evidences:

1. **Cold-start recall** — fresh session retrieves earlier state, in **one
   unedited demo segment with timestamp**
2. **Critical-path calls** — README identifies memory read/write locations
   **findable within two minutes**
3. **Deletion test** — removing memory calls breaks the core function

Rubric scores are only assigned to gate-passing projects, and averaged
**across judges who approved**. So a split panel doesn't just cost points; a
tie kills the entry.

**We are strong here.** #2 and #3 are done and independently verified
(`DEEP_INTEGRATION_REPORT.md`, `FINAL_AUDIT_DONE.md`). #1 exists as a script
that passes — but not as video, which is what the rule actually asks for.

## 3. How an AI judge differs from a human one

No AI-judge methodology is disclosed on either page — it says "a panel of
judges". Plan for both, because the failure modes are almost opposite.

### What an LLM judge is unusually good at

- **Catching contradictions inside its context.** This is its single sharpest
  capability. Two claims that disagree anywhere in the same README get
  flagged with near-perfect recall. Humans skim past these.
- **Rubric mapping.** Given five criteria, it will look for five answers. Text
  that doesn't name the criterion often scores as if the work doesn't exist.
- **Distinguishing specific from vague.** "~1.04ms measured over 4 runs"
  reads as evidence; "blazing fast" reads as noise. It is quite hard to
  bluff.

### What an LLM judge cannot do

- **Verify anything externally.** It cannot curl your endpoint, run your
  tests, or check a transaction on Basescan. Every claim is either
  self-evidencing *in the text* or it is unsupported.
- **Read 10,790 tokens of README.** Ours is that long. A judging harness
  typically allocates a few thousand tokens per document. **Everything past
  roughly line 550 may never be seen.**
- **Infer structure.** It will not reconstruct your architecture from prose
  scattered across twelve sections.

### The strategic consequence

The two capabilities invert the usual advice:

> **For an AI judge, pasted command output beats polished prose, and the top
> of the file beats the whole of it.**

This is why the audit documents are a genuine asset rather than overhead.
`DEEP_INTEGRATION_REPORT.md` contains file:line citations and pasted output —
exactly the shape an LLM scores as verified. Prose claiming the same thing
would score lower.

It also means **every contradiction is a live liability**, because the one
judge guaranteed to find it is the automated one.

---

## 4. Critical defects, by severity

### 🔴 S1 — The README sends judges to the wrong repository

`README.md:48`:

```html
<a href="https://github.com/LSUDOKO/Vigil">Source</a>
```

and again at `README.md:1258` in the clone instructions.

```
LSUDOKO/Vigil     pushed 2026-08-18   MEMORY.md: 404
notwen123/VIGIL-  pushed 2026-08-25   MEMORY.md: 200
```

**That repo has no MEMORY.md and predates every commit of the memory work.**
A judge who clicks "Source" — the obvious link, in the header, above
everything — lands on a codebase with zero Sibyl Memory in it.

Against the gate that is not a lost point. It is *"Memory not load-bearing"*
→ **disqualification**. This is the highest-expected-loss item in the entire
submission and it is a two-line fix.

### 🔴 S2 — The live-demo links are wrong or dead

`README.md:46-47`:

| Link in README | Status | Should be |
|---|---|---|
| `vigil-featherless.vercel.app` | 200 — **wrong app** | `vigil-sibyl-memory.vercel.app` |
| `vigil-server.onrender.com` | **404 dead** | `vigil-cuy2.onrender.com` |

A human judge clicks, gets a 404, and marks technical execution down. An AI
judge can't click — but if the submission form lists different URLs than the
README, it flags the inconsistency.

### 🟠 S3 — License badge contradicts the license

`README.md:57` renders a **MIT** badge. Root `LICENSE` is **Apache-2.0**.
Twenty-six lines above the badge, the text correctly explains the dual
arrangement.

Both licenses are OSI-approved, so this is not itself a DQ — but
*"Non-OSI-approved license"* **is** a listed disqualifier, which means
licensing is something judges explicitly check. Handing the checker a
self-contradiction on the exact axis they're auditing is a bad trade for a
decorative badge.

### 🟠 S4 — Demo video does not exist

Listed under *"Missing required submission materials"* → **disqualification**,
and it carries the entire 15-point presentation criterion, plus gate evidence
#1 (cold-start recall, unedited, timestamped).

**~15 points plus a DQ risk, for about 20 minutes of work.** Nothing else on
this list has that ratio.

### 🟠 S5 — Two public posts do not exist

Also listed under required materials. Must tag **@sibylcap** and partners.
Minimum: demo video + build log.

### 🟡 S6 — README is 10,790 tokens with the good part buried

Lines 1–31 are strong. Then ~1,450 lines of legacy VIGIL README follow. An
AI judge may never reach `MEMORY.md`'s file:line tables — the thing that wins
the 40-point criterion — unless the top of the README makes the pointer
unmissable.

---

## 5. Honest score projection

| Criterion | Now | Ceiling | What moves it |
|---|---|---|---|
| **Gate** | PASS (if judges see the right repo) | — | Fix S1. Then video for evidence #1 |
| Memory load-bearing (40) | **34–38** | 40 | Already exceptional: 5 tiers live, deletion test executed, ordering guarded by a test. "Dynamic-storage patterns" = our progressive ladder + archive tier |
| Innovation (25) | **19–22** | 25 | Trust-as-memory for a firewall is genuinely novel. "Clever but unusable" is the trap — the live deployment is the defence |
| Technical execution (20) | **16–19** | 20 | 62 commits, 12 packages green, live deploy. Cost: stale links, badge conflict |
| Pitch (15) | **0** | 13–15 | **No video.** This is a zero right now |
| PMF (+10) | **3–5** | 8–10 | Real waitlist ✓. No design partners, no pilots, no usage numbers |
| **Multiplier** | **×1.15** | ×1.25 | Base verified. Virtuals is the open question — see below |

**Now: ~83 × 1.15 ≈ 95.**
**With video + posts + link fixes: ~105 × 1.15 ≈ 121.**
**If Virtuals also counts: ~105 × 1.25 ≈ 131.**

## 6. The Virtuals question is worth ~10 points — ask, don't assume

The rules define a qualifying Virtuals stack as:

> "**ACP job**, registered/transacting agent, **or** native integration
> exercised in demo"

Three alternatives, disjunctive. We demonstrably have the first: a real ACP
job POSTed to the deployed bridge, forwarded to the Go engine, decided from
recalled trust:

```
job=final-audit-2 buyer=trading-agent-alpha verdict=BLOCK trust=10
  recall=2.292ms source=sibyl_memory(...)
```

Whether that counts, or whether "ACP job" means a job transacted on the ACP
network by a registered agent, decides ×1.15 vs ×1.25.

**Ask the organizers directly.** Do not self-award it — *"Fabricated evidence
is a disqualification, including after payout"*, and unused claimed stacks
forfeit the bonus outright. A one-line question in their Discord is worth
about ten points.

Note also: *"Stacks must perform real work **in the demo**."* Our Base
anchoring is real and verified — but if it isn't **on screen in the video**,
it may forfeit even the ×1.15. **The video must show a transaction being
sent, not just describe one.**

---

## 7. SEO — for the human judge, and for after

Current state:

```
GitHub description : None
GitHub topics      : []
Stars              : 0
```

An empty description and no topics means the repo is invisible in GitHub
search and gives a judge no orientation before they open it.

**Set the description** to something that states the mechanism, not the
category:

> Runtime firewall for AI agents that remembers banned agents across
> restarts. 5-tier SQLite/Postgres memory, sub-ms recall, no vectors,
> Base-anchored audit trail.

**Add topics:** `ai-agents` `agent-security` `llm-security` `runtime-firewall`
`memory` `sibyl-memory` `base` `virtuals-acp` `mcp` `observability` `golang`.

**The site meta is off-message.** The Vercel deployment says:

```
<title>VIGIL — AI Runtime Governance & Control Plane</title>
<meta name="description" content="VIGIL intercepts every AI tool call before
execution, evaluates enterprise policies in milliseconds…">
```

**It does not mention memory once.** The entire submission is about memory,
and the public front door doesn't say so. Anyone — human or crawler — who
lands there learns about a governance product, not a memory product.

Also missing: `robots.txt` (404), `sitemap.xml` (404).

## 8. LEO — being citable by language models

LEO (also called GEO or AEO) is optimizing to be **retrieved and quoted by
LLMs** rather than ranked by a search engine. The mechanics differ:

| | SEO | LEO |
|---|---|---|
| Unit | Page | **Passage** |
| Wins by | Links, authority | **Quotability + specificity** |
| Rewards | Keywords | **Self-contained factual claims** |
| Punishes | Thin content | **Vagueness, unresolved pronouns** |

This matters here for a concrete reason: **if any part of the judging is
AI-assisted, it is running LEO on your repo whether you optimised for it or
not.** The same properties that make a passage citable make it scoreable.

**What already works in our favour:**

- Claims carry their own evidence (`file:line`, pasted output, tx hashes)
- Numbers are specific and qualified by condition (~1 ms in-process /
  ~260 ms hosted) — an LLM can quote either without misrepresenting
- Contradictions have been actively removed
- Each document states its own method up front

**What to add, in order of value:**

1. **`llms.txt` at the site root** (currently 404). The emerging convention
   for telling models what a project is, in plain markdown. Roughly:

   ```
   # VIGIL MEMORY
   > Runtime firewall for autonomous AI agents with cross-session trust memory.

   An agent blocked for attempting a typosquat stays blocked after a restart,
   because trust is a database row rather than context-window history.
   Recall is a keyed lookup: ~1ms in-process, ~260ms on the hosted split
   deployment. No vectors, no embeddings, no LLM on the enforcement path.

   ## Docs
   - [Memory map with file:line](https://github.com/notwen123/VIGIL-/blob/main/MEMORY.md)
   - [Deep integration audit](https://github.com/notwen123/VIGIL-/blob/main/DEEP_INTEGRATION_REPORT.md)
   ```

2. **A one-paragraph, self-contained definition** at the very top of the
   README — no pronouns, no dependency on earlier text. This is the passage
   that gets quoted.

3. **Front-load the numbers.** "Blocks a repeat offender in ~1 ms with zero
   LLM calls, verified across a process restart" is quotable. "Fast and
   reliable" is not.

4. **Name the entity consistently.** "VIGIL MEMORY" everywhere, not VIGIL /
   Vigil / vigil-sibyl-memory / Wraith. Inconsistent naming fragments the
   entity across a model's retrieval.

5. **FAQ-shaped headings.** "Does memory survive a restart?" retrieves better
   than "Persistence Architecture", because it matches how questions are
   actually asked.

---

## 9. Do these, in this order

| # | Action | Time | Value |
|---|---|---|---|
| 1 | Fix `README.md:48` + `:1258` → `notwen123/VIGIL-` | 2 min | **Prevents DQ** |
| 2 | Fix `README.md:46-47` live links | 2 min | Prevents a dead 404 in front of a judge |
| 3 | Fix or drop the MIT badge at `:57` | 1 min | Removes a contradiction on an audited axis |
| 4 | **Record the demo video** — must show a Base tx being sent | 20 min | **~15 pts + gate evidence + protects ×1.15** |
| 5 | Two posts tagging @sibylcap | 10 min | Required material |
| 6 | Ask organizers whether our ACP job qualifies | 5 min | **~10 pts** |
| 7 | GitHub description + topics | 3 min | SEO + judge orientation |
| 8 | Site `<title>`/meta to say *memory* | 5 min | On-message front door |
| 9 | `llms.txt` | 5 min | LEO |
| 10 | One design partner or usage number | varies | PMF 4 → 8 |

Items 1–3 are pure downside-removal and cost five minutes total. Item 4 is
the single largest scoring gap in the submission.

**Not on this list, deliberately:** inventing design partners, self-awarding
the Virtuals multiplier, or claiming mainnet. Each is explicitly a
disqualifier, *including after payout*.
