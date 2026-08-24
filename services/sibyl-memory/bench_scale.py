#!/usr/bin/env python3
"""Does trust recall stay sub-millisecond at scale?

VIGIL MEMORY's headline claim is that an agent can accumulate hundreds of
sessions and still be recognised in about a millisecond, because trust is a
keyed lookup in a SQLite file rather than history replayed into a context
window. That claim was asserted long before it was measured. This measures it.

    python services/sibyl-memory/bench_scale.py            # 500 agents
    python services/sibyl-memory/bench_scale.py --agents 100 --events 200

The shape deliberately mirrors the claim: 500 agents each with 1,000 journal
events is the rough equivalent of 500 sessions of dense history. What is timed
is the operation the firewall actually performs on the hot path —
get_entity(category, name) — not a full-table scan, because that is not what
enforcement does.

Reports p50/p95/p99 and the worst single call. p95 matters more than the mean:
a firewall that is usually fast and occasionally slow is a firewall that
occasionally stalls an agent.
"""

from __future__ import annotations

import argparse
import os
import random
import statistics
import sys
import tempfile
import time
from pathlib import Path

from sibyl_memory_client import MemoryClient


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--agents", type=int, default=500)
    ap.add_argument("--events", type=int, default=1000, help="journal events per agent")
    ap.add_argument("--samples", type=int, default=1000, help="timed recalls")
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--keep", action="store_true", help="keep the database file")
    args = ap.parse_args()

    rng = random.Random(args.seed)
    tmp = Path(tempfile.mkdtemp(prefix="sibyl-bench-")) / "memory.db"
    c = MemoryClient.local(str(tmp))

    total_events = args.agents * args.events
    print("=" * 66)
    print("VIGIL MEMORY — scale benchmark")
    print("=" * 66)
    print(f"database : {tmp}")
    print(f"target   : {args.agents} agents x {args.events} journal events "
          f"= {total_events:,} rows")
    print()

    # --- write phase --------------------------------------------------------
    print(f"[1/3] writing {args.agents} agent trust records (WARM)…")
    t0 = time.perf_counter()
    for i in range(args.agents):
        c.set_entity("agent", f"agent-{i:05d}", {
            "trust_score": rng.randint(0, 100),
            "total_blocks": rng.randint(0, 5),
            "banned_tools": ["run_command"] if i % 7 == 0 else [],
            "last_violation_type": "typosquat" if i % 7 == 0 else "",
        })
    warm_s = time.perf_counter() - t0
    print(f"      {args.agents} entities in {warm_s:.1f}s "
          f"({args.agents / warm_s:,.0f}/s)")

    print(f"[2/3] writing {total_events:,} journal events (COLD)…")
    t0 = time.perf_counter()
    for i in range(args.agents):
        name = f"agent-{i:05d}"
        for j in range(args.events):
            c.write_event(
                acted=[f"BLOCK run_command #{j}"],
                extra={"agent_id": name, "session_id": f"s-{j}", "reason": "typosquat"},
            )
        if (i + 1) % max(1, args.agents // 10) == 0:
            print(f"      {(i + 1) * args.events:,}/{total_events:,} events…")
    cold_s = time.perf_counter() - t0
    print(f"      {total_events:,} events in {cold_s:.1f}s "
          f"({total_events / cold_s:,.0f}/s)")

    size = tmp.stat().st_size
    wal = tmp.with_suffix(tmp.suffix + "-wal")
    size += wal.stat().st_size if wal.exists() else 0
    print(f"      on disk: {size:,} bytes ({size / total_events:.0f} B/event)")
    print()

    # --- read phase ---------------------------------------------------------
    # A fresh client, so the first reads pay a cold open rather than
    # inheriting warm in-process state from the writer above.
    print(f"[3/3] timing {args.samples} random recalls on a FRESH client…")
    c2 = MemoryClient.local(str(tmp))
    targets = [f"agent-{rng.randrange(args.agents):05d}" for _ in range(args.samples)]

    timings_ms: list[float] = []
    for name in targets:
        t = time.perf_counter()
        rec = c2.get_entity("agent", name)
        timings_ms.append((time.perf_counter() - t) * 1000)
        # Touch the payload so the read cannot be optimised away.
        assert rec["body"]["trust_score"] >= 0

    timings_ms.sort()
    p = lambda q: timings_ms[min(len(timings_ms) - 1, int(len(timings_ms) * q))]

    print()
    print("-" * 66)
    print("RESULT")
    print(f"  rows in database   : {total_events + args.agents:,}")
    print(f"  recalls timed      : {args.samples:,}")
    print(f"  p50                : {p(0.50):.3f} ms")
    print(f"  p95                : {p(0.95):.3f} ms")
    print(f"  p99                : {p(0.99):.3f} ms")
    print(f"  worst              : {timings_ms[-1]:.3f} ms")
    print(f"  mean               : {statistics.fmean(timings_ms):.3f} ms")
    print(f"  LLM calls          : 0")
    print(f"  embeddings/vectors : 0")
    print("-" * 66)

    sub_ms = p(0.95) < 1.0
    print(f"\nCLAIM: 'still recalls in about a millisecond at scale'")
    print(f"VERDICT: p95 = {p(0.95):.3f} ms -> "
          f"{'SUPPORTED' if sub_ms else 'NOT sub-millisecond at p95'}")
    if not sub_ms:
        # Reported rather than hidden. A p95 above 1ms is still far below any
        # embedding round-trip, but the claim as written would be wrong and
        # should be restated rather than defended.
        print("         Restate the claim with this measured number.")

    if not args.keep:
        for f in (tmp, wal, tmp.with_suffix(tmp.suffix + "-shm")):
            if f.exists():
                os.unlink(f)
    else:
        print(f"\ndatabase kept at {tmp}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
