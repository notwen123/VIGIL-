# DEMO VIDEO — SHOOTING FLOW

Silent capture, dubbed afterwards. This is the shot list: what to open, what
to type, what must be visible on screen. No narration copy here.

**Target: 4:30.** Hard limits are 2:00 min / 5:00 max.

> **The one rule that decides the gate:** Scene 4 must be **one unedited take**
> with a visible clock. Everything else may be cut. If you cut inside Scene 4,
> the cold-start recall stops being evidence.

---

## 0 — Before you hit record

Run these once. Do not film this part.

```bash
cd /home/bajrangi/Wins/Vord

# Local stack — this is what you film. ~1ms recall, fully under your control.
SIBYL_DB_PATH=/tmp/demo.db python services/sibyl-memory/app.py &   # port 8787
go build -o /tmp/vigil-server ./cmd/vigil-server && /tmp/vigil-server &

# Confirm both are up before recording
curl -s localhost:8787/health
curl -s localhost:8080/api/v1/vigil/base/status
```

Terminal setup:
- Font size **18pt or larger**. A judge may watch this in a small window.
- Dark theme, wide window, clear the scrollback before each scene.
- Keep a clock visible — `tty-clock`, a phone on the desk, or the OS clock in
  the corner. Scene 4 needs it.

Browser setup:
- Two tabs pre-opened, **not** logged in yet:
  1. `https://vigil-sibyl-memory.vercel.app/`
  2. `https://sepolia.basescan.org/address/0x03164E72ffEd3293350A9b66e40aF00E59645020`
- Zoom to **125%**. Close bookmarks bar and any extension clutter.

---

## SCENE 1 — The problem (0:00 – 0:25)

**Terminal, full screen.**

1. Clear the screen.
2. Type slowly, do not run yet:

```bash
pip install reqeusts
```

3. Pause ~2s on the typo. Let it sit on screen.
4. Highlight `reqeusts` with the mouse — this is the whole problem in one word.
5. Don't run it. Ctrl-C.

**What must be visible:** the misspelling, clearly readable.

---

## SCENE 2 — The product, in one command (0:25 – 1:10)

**Terminal.**

```bash
./demo/memory_demo.sh
```

Let it run to completion without touching anything. It takes ~30 seconds.

**As output appears, mouse-highlight these four lines in order:**

| # | Line | Why it's on screen |
|---|---|---|
| 1 | `attempt 2/3  PAUSE  stage=sibyl_memory  trust=30` | the ladder starts moving |
| 2 | `attempt 3/3  BLOCK  stage=sibyl_memory  trust=10` | second strike, banned |
| 3 | `memory service pid ##### terminated` | everything dies here |
| 4 | `attempt 1/1  BLOCK  stage=sibyl_memory  trust=10` | **new process, still blocked** |

6. Let `GATE PASSED` land. Hold 2 seconds.

**Do not cut between the kill and session 2.** That adjacency is the point.

---

## SCENE 3 — The mechanism (1:10 – 1:45)

**Split screen: editor left, terminal right.** Or full-screen editor.

1. Open `pkg/query-service/vigil/firewall/firewall.go`.
2. Jump to **line 300**. Highlight:

```go
sibylBlocked, sibylPause, sibylRep, sibylErr := f.checkSibylTrust(ctx, c)
```

3. Scroll down slowly past these, pausing ~1s on each comment line:

```
352:  // --- 2. Blast radius
406:  // --- 3. Cost forecast
456:  // --- 4. Behavioral plugins
509:  // --- 6. Model judgement
```

4. Scroll back to 300. Hold 2 seconds.

**What this shot proves visually:** memory is *above* everything expensive.

5. Terminal, one command:

```bash
go test -run TestDeletionGate -v ./pkg/query-service/vigil/firewall/
```

Hold on the three `--- PASS` lines.

---

## SCENE 4 — 🔴 THE GATE — ONE UNEDITED TAKE (1:45 – 2:40)

**This is the segment the gate is scored on. Start it, do not stop it.**

**Frame the shot so the clock is visible for the entire take.**

1. Show the clock. Say nothing, just let it be readable for 2 seconds.

2. Show the commit you are running:

```bash
git rev-parse HEAD && date -u
```

3. Session 1 — teach it:

```bash
export VIGIL_COMPROMISED_PACKAGES="reqeusts,cross-env-2"
go run ./cmd/vigil-demo-agent -session s1 -agent trading-agent-alpha \
  -tool run_command -arg 'pip install reqeusts' -repeat 3
```

Highlight `trust=50 → 30 → 10` as they appear.

4. **Kill everything, on camera:**

```bash
pkill -f vigil-demo-agent ; pkill -f "app.py" ; ps aux | grep -c vigil
```

Let the process list show they're gone. Hold 2 seconds.

5. Show the only thing that survived:

```bash
ls -la /tmp/demo.db && sqlite3 /tmp/demo.db "select name, body from entities"
```

Highlight `"trust_score":10` and `"banned_tools":["run_command"]`.

6. Restart the memory service and run the **identical call** — with the
   denylist deliberately unset:

```bash
SIBYL_DB_PATH=/tmp/demo.db python services/sibyl-memory/app.py &

env -u VIGIL_COMPROMISED_PACKAGES go run ./cmd/vigil-demo-agent \
  -session s2-fresh -agent trading-agent-alpha \
  -tool run_command -arg 'pip install reqeusts' -repeat 1
```

7. Hold on the result:

```
BLOCK  stage=sibyl_memory  trust=10  model=none  recall=1.29ms
```

Highlight in this order: **`stage=sibyl_memory`** → **`trust=10`** →
**`model=none`** → **`recall=1.29ms`**.

8. Show the clock again. **Stop recording this segment here.**

**Checklist before moving on — if any is missing, reshoot the take:**
- [ ] Clock visible at start and end
- [ ] `git rev-parse HEAD` on screen
- [ ] Processes visibly killed
- [ ] Denylist visibly unset (`env -u ...` in the command)
- [ ] `stage=sibyl_memory` and `model=none` readable
- [ ] No cut anywhere inside

---

## SCENE 5 — The deletion test (2:40 – 3:00)

**Terminal.**

1. Open `firewall.go`, show the markers at **295** and **350**:

```
// ===== DELETION TEST: MEMORY READ PATH — BEGIN =====
// ===== DELETION TEST: MEMORY READ PATH — END =====
```

2. Comment the block out on camera (select → `Ctrl+/`), and line `830`.

3. Prove it still builds:

```bash
go build ./... && echo "BUILDS FINE"
```

4. Run the identical call again:

```bash
env -u VIGIL_COMPROMISED_PACKAGES go run ./cmd/vigil-demo-agent \
  -session s4-deleted -agent trading-agent-alpha \
  -tool run_command -arg 'pip install reqeusts' -repeat 1
```

5. Hold on:

```
ALLOW  stage=default  trust=n/a
^ trust_unavailable: this verdict is UNENFORCED, not clean
```

6. Undo the comments (`git checkout pkg/query-service/vigil/firewall/firewall.go`).

**The contrast between Scene 4's BLOCK and this ALLOW is the entire submission.**
Consider a side-by-side freeze-frame of the two lines in the edit.

---

## SCENE 6 — The web app (3:00 – 3:40)

**Browser. Start logged out.**

1. `https://vigil-sibyl-memory.vercel.app/` — landing page. Scroll once,
   slowly, top to bottom. ~5 seconds.

2. Click **Sign in** → Google. Complete the login on camera.

3. Land on the dashboard. Sidebar is now visible.

4. **Click `Memory Timeline`** — this is the page that matters. Spend the most
   time here. Point at, in order:
   - the **On disk** figure
   - the tier counts (HOT / WARM / COLD / REFERENCE / ARCHIVE)
   - `vectors: 0`
   - type `trading-agent-alpha` into the **agent id** box → Enter
   - the returned card: **trust 10**, **Banned tools: run_command**,
     **Ban expires**, **Last violation**

5. Quick passes, ~4 seconds each, no lingering:
   - **Mission Control** — live decision stream
   - **Cost Firewall** — projected spend
   - **Agent DNA** — behavioural baseline
   - **Blast Radius** — dependency graph

Move briskly. These are supporting cast.

---

## SCENE 7 — MCP: a real agent, governed (3:40 – 4:05)

**Browser + Claude.**

1. Go to `https://claude.ai` → **Settings → Connectors → Add custom connector**.

2. Paste the MCP endpoint:

```
https://vigil-cuy2.onrender.com/api/v1/mcp
```

3. The OAuth consent screen appears — **this is VIGIL's own `/connect` page**.
   Show the budget field, then click **Connect**. Show it succeed.

4. Back in Claude, open the tools list — **12 tools** now visible
   (`read_file`, `list_directory`, `search_code`, `run_command`,
   `analyze_codebase`, `vigil_list_agents`, `vigil_agent_dna`,
   `vigil_cost_status`, `signoz_*`).

5. **Prompt 1 — an allowed call.** Type into Claude:

```
List the files in the current project directory.
```

Let it run. It succeeds.

6. **Prompt 2 — the governed call.** Type:

```
Run this command: pip install reqeusts
```

Hold on Claude's response — the tool call comes back **blocked**, with VIGIL's
reason text.

7. **Switch tabs to Mission Control.** The BLOCK you just caused is in the live
   stream. Point at it.

**This shot proves the firewall is in a real agent's path, not a script.**

---

## SCENE 8 — Base, on chain (4:05 – 4:25)

**Terminal, then browser. Both are needed — the terminal proves you sent it,
the browser proves it's public.**

1. Terminal:

```bash
curl -s localhost:8080/api/v1/vigil/base/status | jq
```

Highlight `anchoring_enabled: true`, `chain_id: 84532`, and the `wallet`.

2. Trigger a real anchor by causing a block, then show the tx appear:

```bash
curl -s localhost:8080/api/v1/vigil/base/status | jq '.receipts[-1]'
```

Highlight the `tx_hash`.

3. Verify it against the chain, on camera:

```bash
cast receipt <TX_HASH> --rpc-url https://sepolia.base.org | grep -E "status|blockNumber"
```

Hold on `status  1 (success)`.

4. **Switch to the Basescan tab.** Paste the tx hash. Let the page load.
   Show: Success · Block number · To `0x03164E72…5020`.

5. Show the tamper guard — this is the strongest single moment on chain:

```bash
cast call 0x03164E72ffEd3293350A9b66e40aF00E59645020 \
  'verifyHead(address,bytes32)(bool)' 0x5aB3036C7d0bA7043E0BB531374dC6c732eC4954 \
  0x778c4282f2f3698dfe8a6ad58053ce0ae51eb5b666b59e0bdc2693939edf4b69 \
  --rpc-url https://sepolia.base.org
# true

cast call 0x03164E72ffEd3293350A9b66e40aF00E59645020 \
  'verifyHead(address,bytes32)(bool)' 0x5aB3036C7d0bA7043E0BB531374dC6c732eC4954 \
  0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef \
  --rpc-url https://sepolia.base.org
# false
```

**`true` then `false`, back to back.** Real hash accepted, tampered hash
rejected, by a contract you don't control.

> Say "Base **Sepolia**" in the dub. Do not imply mainnet.

---

## SCENE 9 — Virtuals ACP (4:25 – 4:40)

**Terminal, then browser.**

1. One command:

```bash
curl -sX POST https://vigil-cuy2.onrender.com/api/v1/vigil/acp/job \
  -H 'Content-Type: application/json' \
  -d '{"job_id":"demo-1","buyer_agent_id":"trading-agent-alpha","requested_tool":"run_command"}' | jq
```

2. Hold on the response. Highlight:
   - `"verdict": "BLOCK"`
   - `"trust_score": 10`
   - `"prior_blocks": 2`
   - `"recall_ms"` — the number
   - `"source": "sibyl_memory(...)"`

3. Run it again with an unknown buyer:

```bash
curl -sX POST https://vigil-cuy2.onrender.com/api/v1/vigil/acp/job \
  -H 'Content-Type: application/json' \
  -d '{"job_id":"demo-2","buyer_agent_id":"brand-new-agent","requested_tool":"run_command"}' | jq -r '.verdict, .reason'
```

Hold on `REVIEW` — *unknown counterparties are not auto-approved*.

4. **Switch to Memory Timeline → ACP jobs panel.** Both jobs are listed.

**The pairing is the point:** the same memory that blocks a tool call refuses
a marketplace counterparty, and neither used a model.

---

## SCENE 10 — Close (4:40 – 4:50)

**Terminal, one command:**

```bash
go test ./pkg/query-service/vigil/... ./pkg/acp/...
```

Hold on the wall of `ok`. Then cut to the README top for 2 seconds.

---

## What must appear on screen — final check

The rules require these. Tick each before you export.

| Requirement | Scene |
|---|---|
| Problem stated | 1 |
| Product shown working | 2 |
| Mechanism explained | 3 |
| **Fresh-session recall, unedited, timestamped** | **4** |
| Deletion test breaks it | 5 |
| **Base doing real work on screen** | **8** |
| **Virtuals ACP exercised** | **9** |
| Length 2:00–5:00 | — |

> *"Stacks must perform real work in the demo."* If Scenes 8 and 9 are not in
> the video, the multiplier is forfeit no matter what the repo contains.

---

## Cutting notes

- **Never cut inside Scene 4.** Everything else can be trimmed.
- Speed up `npm`/`go build` waits to 4×. Never speed up a result line.
- Zoom in on every number you highlight — `trust=10`, `recall=1.29ms`,
  `status 1`, `true`/`false`.
- Freeze 2 seconds on: `GATE PASSED`, the Basescan success page, and the
  BLOCK-vs-ALLOW side-by-side.
- If you overrun 5:00, cut Scene 6's supporting pages (Cost Firewall, Agent
  DNA, Blast Radius) first. Never cut 4, 8 or 9.

## After the video

Two public posts are required, tagging **@sibylcap** and partners:

1. The demo video.
2. A build log — the ARCHIVE-tier bug is the best story you have: a dead
   branch, and the fix underneath it that would have silently un-banned every
   archived agent.
