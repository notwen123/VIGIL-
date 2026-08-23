# VIGIL MEMORY — demo video script

Total: ~2:45. The 1:00–1:50 segment must be **one unbroken take** — that segment
is the evidence, and a cut in the middle of it is exactly what a sceptical
viewer would assume was hiding the trick.

**Before recording**

```bash
git rev-parse --short HEAD          # put this on screen at 1:30
rm -f /tmp/vigil-memory-demo.db     # start from genuine zero
```

Terminal font large enough to read PIDs. Wall clock visible if possible.

---

## 0:00–0:30 — The problem

> "This is an autonomous agent with shell access. It tries to install
> `reqeusts` — a typosquat of `requests`. VIGIL catches it, and charges an
> LLM call to work out why."

Show the block.

> "Now I kill the terminal."

`Ctrl-C`. Restart. Run the identical command.

> "It's caught again — and paid for again. VIGIL has no idea it already
> answered this exact question. Every restart is amnesia, and amnesia has a
> bill attached."

---

## 0:30–1:00 — What we built

> "VIGIL MEMORY puts trust in a SQLite file instead of a context window.
> Not a vector store — no embeddings, no API key, no network. One file."

Show the enforcement order on screen:

```
deterministic  →  MEMORY  →  HydraDB graph  →  Featherless LLM
   instant        ~1ms         375ms–1s          seconds, $$$
```

> "Memory is second. If it recognises the agent, the graph query and the
> model call never happen at all."

---

## 1:00–1:50 — THE PROOF (single unbroken take)

Run:

```bash
./demo/memory_demo.sh
```

Narrate over the live output — do not cut:

**1:00 — session 1**
> "Three attempts. First one blocked by the denylist. Watch trust: 50, then
> 30, then 10. Second violation earns a 24-hour ban on `run_command`, and
> that ban is written to disk, not to RAM."

**1:25 — the kill**
> "The process is dead. Point at the byte count — that's all that's left."

**1:30 — show the commit hash**
```bash
git rev-parse --short HEAD && date -u
```
> "Commit and timestamp, on screen, unedited."

**1:35 — session 2**
> "Brand new processes. And note — the denylist is *not* set this time.
> Nothing but memory can block this."

> "BLOCK. Trust 10, recalled in about one and a half milliseconds. Zero LLM
> calls. Zero graph queries. It has never seen this agent, and it stopped it
> anyway."

---

## 1:50–2:20 — The deletion test

Still rolling:

> "Now the part that decides whether any of this was real."

The script runs it automatically (`VIGIL_SIBYL_DISABLED=1`):

> "Same agent. Same command. Memory severed — and *nothing else changed*.
> ALLOWED. The typosquat goes straight through."

> "That's the gate. Take the memory out and VIGIL stops being able to do the
> thing it's for. Notice it also flags the verdict `trust_unavailable` —
> unenforced, not clean. It doesn't quietly pretend it checked."

Optionally show the stronger version:

```bash
go test -run TestDeletionGate -v ./pkg/query-service/vigil/firewall/
```

> "Three tests. One of them passes only when the product *fails* — that's
> what makes it evidence rather than a claim."

---

## 2:20–2:45 — Partners and who it's for

**ACP** — live:
```bash
curl -s localhost:8080/api/v1/vigil/acp/job -d \
  '{"job_id":"acp-001","buyer_agent_id":"trading-agent-alpha","requested_tool":"run_command"}'
```
> "A marketplace counterparty. Refused, citing trust 10 and two prior
> violations — because that history outlived the sessions it happened in. A
> stateless provider would have to trust everyone or no one."

**Base** — be straight about it:
> "Audit anchoring and x402 are code-complete and gated on a signer. We
> haven't run them on-chain and there's no transaction hash to show, because
> there's no funded wallet. The status endpoint says so rather than
> inventing a receipt."

**Close:**
> "Built for teams whose agents spend money — trading agents on Base, CI
> agents with install rights. The pain is: the agent restarts and forgets
> the limit it was corrected for last week. Memory in a file, not a context
> window — 500 sessions, 115k tokens each, still recalls trust 10 in a
> millisecond."

---

## Do not say

- ❌ "on-chain verified" / any tx hash — none exist yet
- ❌ named design partners — the README slots are deliberately TODO
- ❌ "MEV-proof", "unhackable", "impossible to bypass"
- ❌ a latency number you didn't just read off the screen

If a number isn't on screen, don't say it.
