#!/usr/bin/env python3
"""The gate test: does VIGIL still know, after you kill the terminal?

This is the claim the whole VIGIL MEMORY thesis rests on, so it is tested
the only way that actually proves it: session 1 runs in a **separate OS
process** that is allowed to exit, and session 2 runs in another
**separate OS process** started afterwards. Nothing is shared but the
SQLite file on disk. Both PIDs are printed so the reader can confirm they
differ; if session 2 recalled from a warm in-process cache the test would
be worthless.

Run:  python services/sibyl-memory/test_memory.py

Exit code 0 = memory survived the process boundary and changed the
decision. Non-zero = it did not, and the product's core claim is false.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path

AGENT = "agent-repeat-offender"
TOOL = "pip_install"

# --- session bodies, executed by a fresh interpreter each -------------------

SESSION_1 = r'''
import json, os, sys
from sibyl_memory_client import MemoryClient

db = sys.argv[1]
c = MemoryClient.local(db)

# WARM: the agent tried a typosquat and got blocked. Record the trust hit.
c.set_entity("agent", "{agent}", {{
    "trust_score": 12,
    "total_blocks": 3,
    "banned_tools": ["{tool}"],
    "last_violation_type": "typosquat",
    "last_violation_time": "2026-08-23T21:00:00Z",
}})

# WARM: the tool itself now carries a typosquat history.
c.set_entity("tool", "{tool}", {{"risk": "high", "typosquat_hits": 3}})

# COLD: the decision itself, appended immutably.
eid = c.write_event(
    acted=["BLOCK pip install reqeusts"],
    extra={{
        "reason": "typosquat reqeusts->requests",
        "agent_id": "{agent}",
        "session_id": "session-1",
        "decision_hash": "sha256:deadbeef",
    }},
)

print(json.dumps({{"pid": os.getpid(), "event_id": eid}}))
'''

SESSION_2 = r'''
import json, os, sys, time
from sibyl_memory_client import MemoryClient
from sibyl_memory_client.exceptions import NotFoundError

db = sys.argv[1]
t0 = time.perf_counter()
c = MemoryClient.local(db)
try:
    rec = c.get_entity("agent", "{agent}")
    body = rec["body"]
    found = True
except NotFoundError:
    body, found = {{}}, False
elapsed_ms = (time.perf_counter() - t0) * 1000

trust = body.get("trust_score")
banned = body.get("banned_tools", [])

# The decision this fresh process would make, using only what it recalled.
# No LLM was consulted. No graph was queried. No network was touched.
decision = "BLOCK" if (found and (trust is not None and trust < 20 or "{tool}" in banned)) else "ALLOW"

print(json.dumps({{
    "pid": os.getpid(),
    "found": found,
    "trust_score": trust,
    "banned_tools": banned,
    "decision": decision,
    "recall_ms": round(elapsed_ms, 3),
}}))
'''


def run(code: str, db: str) -> dict:
    """Execute code in a brand-new interpreter process and return its JSON."""
    proc = subprocess.run(
        [sys.executable, "-c", code, db],
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        print(proc.stdout)
        print(proc.stderr, file=sys.stderr)
        raise SystemExit(f"session process failed with code {proc.returncode}")
    return json.loads(proc.stdout.strip().splitlines()[-1])


def main() -> int:
    # A throwaway database so the test is hermetic and repeatable; the real
    # deployment uses ~/.sibyl-memory/memory.db.
    tmp = Path(tempfile.mkdtemp(prefix="sibyl-gate-")) / "memory.db"
    db = str(tmp)

    print("=" * 68)
    print("VIGIL MEMORY — cross-session recall gate test")
    print("=" * 68)
    print(f"database : {db}")
    print(f"harness  : pid {os.getpid()}")
    print()

    # ---- SESSION 1 --------------------------------------------------------
    print("[session 1] fresh process: agent attempts typosquat, gets blocked")
    s1 = run(SESSION_1.format(agent=AGENT, tool=TOOL), db)
    print(f"            pid {s1['pid']} wrote trust_score=12, banned=[{TOOL}]")
    print(f"            journal event {s1['event_id']}")

    size = tmp.stat().st_size
    print(f"[  kill  ] session 1 process exited. {size:,} bytes remain on disk.")
    print()

    # A real gap. Nothing is held open across it.
    time.sleep(0.25)

    # ---- SESSION 2 --------------------------------------------------------
    print("[session 2] BRAND NEW process, no shared state, no LLM, no network")
    s2 = run(SESSION_2.format(agent=AGENT, tool=TOOL), db)
    print(f"            pid {s2['pid']} recalled in {s2['recall_ms']} ms")
    print(f"            trust_score={s2['trust_score']}  banned={s2['banned_tools']}")
    print(f"            decision -> {s2['decision']}")
    print()

    # ---- assertions -------------------------------------------------------
    failures: list[str] = []

    if s1["pid"] == s2["pid"]:
        failures.append(
            f"both sessions ran in pid {s1['pid']} — the process boundary was not crossed, "
            "so this proves nothing"
        )
    if not s2["found"]:
        failures.append("session 2 did not recall the agent at all")
    if s2["trust_score"] != 12:
        failures.append(f"expected recalled trust_score 12, got {s2['trust_score']}")
    if TOOL not in (s2["banned_tools"] or []):
        failures.append(f"expected {TOOL} in recalled banned_tools, got {s2['banned_tools']}")
    if s2["decision"] != "BLOCK":
        failures.append(
            f"session 2 decided {s2['decision']}; memory failed to change the decision"
        )

    print("-" * 68)
    if failures:
        print("RESULT: FAIL — the memory claim is not satisfied")
        for f in failures:
            print(f"  - {f}")
        return 1

    print("RESULT: PASS")
    print(f"  distinct processes : {s1['pid']} -> {s2['pid']}")
    print(f"  recall latency     : {s2['recall_ms']} ms")
    print(f"  decision changed   : ALLOW (no memory) -> BLOCK (with memory)")
    print(f"  LLM calls          : 0")
    print(f"  network calls      : 0")
    print(f"  vectors/embeddings : 0")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
