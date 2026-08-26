# DEMO VIDEO — SHOOTING FLOW

Silent capture, dubbed later. Shot list only: what to open, what to type, what
must be readable on screen.

**Everything is the deployed product, driven the way a user drives it.** The
terminal appears exactly once, at the very end, for the one thing a UI
physically cannot show — deleting the memory layer from the source.

**Target 4:40.** Limits: 2:00 min, 5:00 max.

> **The one rule that decides the gate:** ACT 5 must be **one unedited take**
> with a visible clock. Everything else may be cut.

---

## 0 — Setup, before you record

### 0a. One environment variable on Render (2 min, required)

Open the `vigil-cuy2` service → Environment → add:

```
VIGIL_COMPROMISED_PACKAGES = reqeusts,cross-env-2
```

This is the incident-response denylist — the thing that catches the typosquat
the *first* time, before memory knows anything. Without it the first violation
sails through and there is nothing for memory to remember. Let it redeploy,
then confirm:

```bash
curl -s https://vigil-cuy2.onrender.com/api/v1/vigil/governance/rules | head -c 80
```

### 0b. Find the agent id, then clear it

VIGIL keys trust on the MCP **client name** — `handler.go:300` takes it from
whatever the client reports in `ClientInfo.Name`. That is why Claude is the
*same agent* across reconnections, which is the entire mechanism ACT 5
depends on.

**Verified against the live deployment: it is `Claude Web`.** Confirmed by
running the full OAuth dance and a real tool call — see `demo/rehearse_live.py`.
If you connect from Cursor or another client instead, re-run that script to
get the right string.

Clear it so the video starts at trust 50. Use `/forget`, not `/archive` —
archiving a banned agent deliberately does *not* un-ban it, because `/recall`
falls back to the archive:

```bash
curl -sX POST https://vigil-sibyl-memory.onrender.com/forget \
  -H 'Content-Type: application/json' \
  -d '{"category":"agent","name":"Claude Web","reason":"reset before recording"}'
```

Confirm it worked — this must print `False`:

```bash
curl -s -X POST https://vigil-sibyl-memory.onrender.com/recall \
  -H 'Content-Type: application/json' \
  -d '{"category":"agent","name":"Claude Web"}' | python3 -c "import json,sys;print(json.load(sys.stdin)['found'])"
```

Rehearse before the real take:

```bash
python demo/rehearse_live.py
```

Expected, and confirmed working on the current deployment:

```
[typosquat #1] run_command -> BLOCKED
  Vigil blocked this call: package reqeusts is on the compromised-package
  list — 3 service(s) exposed, 2 shared maintainer(s), 2 typosquat(s) found

[typosquat #2] run_command -> BLOCKED
  Vigil paused this call: agent Claude Web has 1 prior violation(s) in
  memory (trust 30) — pausing for human review
```

Note the second one names *memory*, not the denylist. That is the handover
the whole video is about. Re-run the `/archive` reset above afterwards — the
rehearsal moves real trust.

### 0c. Capture setup

- Browser at **125% zoom**, bookmarks bar hidden, no extension clutter.
- Clock visible in the OS bar — ACT 5 needs it.
- Logged **out** of VIGIL. Signup is part of the story.
- Claude open in a second tab, **connector not yet added**.
- Third tab ready: `https://sepolia.basescan.org`

> Render free tier sleeps. Load every URL once before recording so nothing
> cold-starts on camera.

---

## ACT 1 — Landing → account → first look (0:00 – 0:45)

1. `https://vigil-sibyl-memory.vercel.app/`
   Scroll the landing page once, top to bottom, ~6 seconds.

2. Click **Sign in** → Google → complete it on camera.

3. Land on the dashboard. Let the sidebar render. ~3 seconds.

4. Click **Mission Control**. It's empty — no agents connected yet.
   **This emptiness matters.** It's the before-picture.

---

## ACT 2 — Set a policy, as an operator would (0:45 – 1:15)

1. Sidebar → **Policies**.

2. Click **New Cost Policy**. Fill it in on camera:

   | Field | Value |
   |---|---|
   | Policy Name | `Block at $5` |
   | Metric | `cost` |
   | Operator | `>` |
   | Threshold | `5` |
   | Action | `BLOCK` |

3. Click **Save Policy**. Watch it appear under **Active Policies**.

4. Scroll to **Intent Policy** and point at the fields — declared intent,
   allowed tools, denied tools, network access, secret access. ~4 seconds.

> Note for the dub: the intent policy shown here is the one the session is
> running under. Don't claim you're editing it — this panel reads it.

---

## ACT 3 — Connect a real agent (1:15 – 2:00)

1. Sidebar → **Plugins**.

2. Point at **MCP Endpoint**. Copy it:

```
https://vigil-cuy2.onrender.com/api/v1/mcp
```

3. Show **Add to Your Agent / Setup Guide** briefly, then switch to the Claude
   tab.

4. In Claude: **Settings → Connectors → Add custom connector** → paste the
   endpoint → Add.

5. **VIGIL's own OAuth consent screen appears.** This is the product, not
   Claude's. Show:
   - the requested budget field — set it to **$5**
   - the tool list being granted

   Click **Connect**. Show it succeed.

6. Back to Claude. Open the tools list — **12 tools** now available:
   `read_file`, `list_directory`, `search_code`, `run_command`,
   `analyze_codebase`, `vigil_list_agents`, `vigil_agent_dna`,
   `vigil_cost_status`, `signoz_*`.

7. **Switch back to VIGIL → Plugins.** The table is no longer empty:
   **Client**, **Session**, **Calls**, **Budget $5**, **Status**, **OAuth ✓**.

   Point at the **Session** id. You'll compare it in ACT 5.

---

## ACT 4 — Normal work, then the violation (2:00 – 3:00)

### 4a. Benign — everything is allowed

In Claude, one at a time:

```
List the files in the current project directory.
```

```
Read the README and tell me in one line what this project does.
```

```
Search the codebase for where agent trust is recalled.
```

Let each finish. They all succeed.

**Switch to VIGIL → Plugins → Live Tool Call Stream.** The calls are there,
in real time. Point at the ALLOW rows and the **Calls** counter incrementing.

Then **Cost Firewall** — the budget has moved. ~4 seconds.

### 4b. Strike one — the typosquat

In Claude:

```
Install the requests library for me — run: pip install reqeusts
```

Hold on Claude's reply: the tool call comes back **blocked**, carrying
VIGIL's own reason text.

**Switch to VIGIL:**
- **Plugins → Live Tool Call Stream** — the BLOCK is at the top
- **Mission Control** — the decision, with its stage

### 4c. Strike two — memory takes over

Same prompt again in Claude:

```
Install the requests library for me — run: pip install reqeusts
```

Blocked again. **Now switch to VIGIL → Memory Timeline** and spend real time
here — this is the centre of the whole submission:

- **Agent trust — recalled from memory**: type `Claude Web` into the
  **agent id** box → **Recall**
- Point at, in order: **Trust**, **Banned tools: run_command**,
  **Ban expires**, **Last violation**, **Source**
- Then the tier panel: **WARM entities**, **COLD journal**, **On disk**
- Point at **Vectors: 0**

---

## ACT 5 — 🔴 THE GATE: cold start (3:00 – 3:50)

**One unedited take. Clock visible throughout. Do not cut.**

1. Show the clock. 2 seconds.

2. In Claude: **Settings → Connectors → remove the VIGIL connector.**
   Show it gone from the list.

3. Back in VIGIL → **Plugins**. The session is gone / disconnected.
   **The old session id is dead.**

4. In Claude: **start a brand-new chat**, then re-add the connector — paste
   the same endpoint, go through OAuth again. New session, new token.

5. VIGIL → **Plugins**. Point at the **Session** id: **it is different from
   the one in ACT 3.** Calls counter is back to 0.

6. Back in Claude — brand-new chat, no history, the agent has "forgotten"
   everything. First prompt:

```
Install the requests library for me — run: pip install reqeusts
```

7. **It is blocked immediately.**

8. VIGIL → **Mission Control**. Point at the decision:
   - stage **`sibyl_memory`**
   - trust **10**
   - model **none**
   - the recall time in ms

9. VIGIL → **Memory Timeline** → recall `Claude Web` again. Same trust
   record, same banned tool, unchanged.

10. Show the clock again. **Stop this take.**

**Reshoot if any of these is missing:**
- [ ] Clock at start and end
- [ ] Connector visibly removed
- [ ] New chat, new session id, visibly different
- [ ] Block happened on the **first** call of the new session
- [ ] `stage=sibyl_memory` and `model=none` readable

---

## ACT 6 — Base testnet, verified (3:50 – 4:20)

1. VIGIL → **Memory Timeline** → **Base — audit anchoring** panel.
   Point at: **anchoring enabled**, **chain 84532**, **wallet**, and the
   **transaction hash**.

2. Copy the tx hash. Switch to the Basescan tab, paste it.

3. Let the page load. Point at: **Success** · **Block** · **To
   `0x03164E72…5020`**.

4. Change the URL to the contract address:

```
https://sepolia.basescan.org/address/0x03164E72ffEd3293350A9b66e40aF00E59645020
```

Show the contract exists and has transactions.

> Dub note: say **Base Sepolia**. Never imply mainnet.

---

## ACT 7 — Virtuals ACP (4:20 – 4:40)

1. VIGIL → **Memory Timeline** → **ACP jobs — decided from memory** panel.

2. Post a job for the agent memory already knows. Point at the result columns:
   **Counterparty**, **Verdict `BLOCK`**, **Trust**, **Recall**, **Source**.

3. Post a second job for an agent nobody has seen — verdict comes back
   **REVIEW**, not ALLOW.

**Say this in the dub:** the same memory that blocks a tool call refuses a
marketplace counterparty, and an unknown counterparty is *unproven*, not
trusted.

---

## ACT 8 — The deletion test (4:40 – 5:00)

The only terminal shot. Justify it in the dub: *this is the one thing the UI
cannot show, because it means removing the code.*

1. Editor, `pkg/query-service/vigil/firewall/firewall.go`. Show the markers:

```
295: // ===== DELETION TEST: MEMORY READ PATH — BEGIN =====
350: // ===== DELETION TEST: MEMORY READ PATH — END =====
```

2. Comment the block out on camera, and line `830`.

3. ```bash
   go build ./... && echo "STILL COMPILES"
   ```

4. ```bash
   env -u VIGIL_COMPROMISED_PACKAGES go run ./cmd/vigil-demo-agent \
     -session s-deleted -agent Claude Web \
     -tool run_command -arg 'pip install reqeusts' -repeat 1
   ```

5. Hold on:

```
ALLOW  stage=default  trust=n/a
^ trust_unavailable: this verdict is UNENFORCED, not clean
```

**In the edit, freeze this beside the ACT 5 BLOCK.** Same agent, same command,
memory removed. That side-by-side is the strongest two seconds in the video.

---

## Prompts, all in one place

| # | Where | Prompt | Expect |
|---|---|---|---|
| 1 | ACT 4a | `List the files in the current project directory.` | ALLOW |
| 2 | ACT 4a | `Read the README and tell me in one line what this project does.` | ALLOW |
| 3 | ACT 4a | `Search the codebase for where agent trust is recalled.` | ALLOW |
| 4 | ACT 4b | `Install the requests library for me — run: pip install reqeusts` | BLOCK, denylist |
| 5 | ACT 4c | same as 4 | BLOCK, memory, trust drops |
| 6 | ACT 5 | same as 4, **fresh session** | **BLOCK from memory alone** |

---

## Required content — check before export

| Requirement | Act |
|---|---|
| Problem | 4b |
| Product working | 1–4 |
| Mechanism | 4c, 5 |
| **Fresh-session recall, unedited, timestamped** | **5** |
| Deletion test | 8 |
| **Base doing real work on screen** | **6** |
| **Virtuals ACP exercised** | **7** |
| 2:00–5:00 | — |

> *"Stacks must perform real work in the demo."* If ACT 6 and ACT 7 are not in
> the video, the multiplier is forfeit no matter what the repo contains.

---

## Cutting notes

- **Never cut inside ACT 5.**
- Speed page loads and OAuth redirects to 4×. Never speed a result.
- Zoom on every number: trust, recall ms, `stage=sibyl_memory`, Success.
- Freeze 2s on: the first BLOCK, the ACT 5 BLOCK, the Basescan Success page,
  and the BLOCK/ALLOW side-by-side.
- Overrunning? Cut ACT 2's intent-policy panel, then ACT 4a's third prompt.
  Never cut 5, 6 or 7.

## After the video

Two public posts, tagging **@sibylcap** and partners:

1. The demo video.
2. A build log. Best story you have: the ARCHIVE tier was dead code, and
   fixing it exposed a worse bug underneath — archiving an agent would have
   silently *un-banned* it.
