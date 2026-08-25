"""Sibyl Memory service — VIGIL's cross-session trust memory.

This is the layer that makes VIGIL remember. Everything else in VIGIL is
stateless across process restarts: kill the terminal and the firewall
forgets that `pip install reqeusts` was already caught as a typosquat, so
the next session pays for the same LLM adjudication to reach the same
conclusion. This service is where that knowledge outlives the process.

Design constraints, all deliberate:

  * Local-first. A single SQLite file on disk (default
    ~/.sibyl-memory/memory.db). No server to provision, no network hop to
    a vector database, no embedding model to host.
  * Zero vectors, zero embeddings. Recall is SQLite FTS5 (porter
    unicode61) over an explicit five-tier schema. Measured cold-open +
    read on a fresh process: ~2ms. An embedding round-trip cannot compete
    with that, and does not need to: agent trust is keyed lookup, not
    semantic similarity.
  * Free and offline. No API key, no quota, no vendor. This is why it can
    sit *ahead* of HydraDB and Featherless in the enforcement order
    instead of behind them.

The HTTP surface below uses VIGIL's own verb names (/remember, /recall,
/archive). The underlying SDK spells three of them differently
(set_entity, get_entity, archive_entity) — verified by introspecting
sibyl_memory_client 0.4.10 directly rather than trusting documentation.
The mapping is done here, once, so the Go caller has one vocabulary.

Five tiers, and what VIGIL puts in each:

  state/      HOT       per-session scratch, rewritten in place every call
  entities/   WARM      durable agent/tool/policy trust records
  journal/    COLD      append-only decision log, one event per decision
  reference/  REFERENCE static runbooks and policy text
  archive     ARCHIVE   entities retired below the trust floor
"""

from __future__ import annotations

import json
import os
import sqlite3
import time
from pathlib import Path
from typing import Any

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field
from sibyl_memory_client import MemoryClient
from sibyl_memory_client.exceptions import NotFoundError

# One process-wide client over one SQLite file. SQLite handles concurrent
# readers fine and the write volume here (one state rewrite + one journal
# append per tool call) is nowhere near contention territory.
DB_PATH = os.environ.get("SIBYL_DB_PATH", "~/.sibyl-memory/memory.db")

# Tenant isolation is a real column in the schema — UNIQUE(tenant_id,
# category, name) — so one database can hold several deployments without
# their agent trust records colliding.
TENANT_ID = os.environ.get("SIBYL_TENANT_ID", "00000000-0000-0000-0000-000000000001")

app = FastAPI(
    title="Sibyl Memory (VIGIL)",
    description="Cross-session trust memory for the VIGIL runtime firewall.",
    version="1.0.0",
)

_client: MemoryClient | None = None


def client() -> MemoryClient:
    global _client
    if _client is None:
        path = Path(DB_PATH).expanduser()
        path.parent.mkdir(parents=True, exist_ok=True)
        _client = MemoryClient.local(str(path), tenant_id=TENANT_ID)
    return _client


# --- request bodies ---------------------------------------------------------


class RememberBody(BaseModel):
    category: str = Field(..., description="agent | tool | policy")
    name: str
    body: dict[str, Any] | list[Any]
    status: str | None = None


class RecallQuery(BaseModel):
    category: str
    name: str


class SearchQuery(BaseModel):
    query: str
    limit: int = 20
    category: str | None = None
    # Restrict to specific tiers, e.g. ["entities"] to search trust records
    # only and skip the far larger journal.
    tiers: list[str] | None = None


class StateBody(BaseModel):
    key: str
    body: dict[str, Any] | list[Any]


class EventBody(BaseModel):
    acted: list[str] | str | None = None
    evaluated: list[str] | str | None = None
    forward: list[str] | str | None = None
    # VIGIL's decision metadata (agent_id, session_id, decision_hash,
    # reason, timestamp) rides here: the SDK's write_event has no
    # dedicated columns for them, so they go in the free-form extra blob
    # rather than being silently dropped.
    extra: dict[str, Any] | None = None


class ReferenceBody(BaseModel):
    key: str
    body: str
    metadata: dict[str, Any] | None = None


class ArchiveBody(BaseModel):
    category: str
    name: str
    reason: str | None = None


# --- endpoints --------------------------------------------------------------


@app.get("/health")
def health() -> dict[str, Any]:
    """Liveness plus the facts a caller needs to trust this instance.

    Reports the resolved database path and whether the file exists yet, so
    a misconfigured SIBYL_DB_PATH surfaces here rather than as silently
    empty recall (which would look exactly like a well-behaved agent with
    no violation history — the single most dangerous failure mode for this
    service).
    """
    path = Path(DB_PATH).expanduser()
    return {
        "status": "ok",
        "db_path": str(path),
        "db_exists": path.exists(),
        "db_bytes": path.stat().st_size if path.exists() else 0,
        "tenant_id": TENANT_ID,
        "schema_version": client().schema_version(),
        "backend": "sqlite+fts5",
        "vectors": False,
    }


@app.post("/remember")
def remember(b: RememberBody) -> dict[str, Any]:
    """WARM tier. Upsert a durable entity (agent trust, tool risk, policy).

    Upsert, not append: UNIQUE(tenant_id, category, name) means an agent's
    trust record has exactly one row that is rewritten as its score moves,
    so recall is a point lookup and there is no history to reduce over.
    """
    started = time.perf_counter()
    rec = client().set_entity(b.category, b.name, b.body, status=b.status)
    return {"ok": True, "entity": rec, "latency_ms": _ms(started)}


@app.post("/recall")
def recall(q: RecallQuery) -> dict[str, Any]:
    """WARM tier. Point-lookup one entity.

    A miss is 'first time we have seen this agent', which is a legitimate
    and common answer — it is returned as found=false rather than raised,
    because the caller's decision differs between 'unknown agent' and
    'service is broken'. Conflating those is how a memory outage silently
    becomes a permissive firewall.
    """
    started = time.perf_counter()
    try:
        rec = client().get_entity(q.category, q.name)
    except NotFoundError:
        rec = _archived_entity(q.category, q.name)
        if rec is None:
            return {"ok": True, "found": False, "entity": None, "latency_ms": _ms(started)}
    return {"ok": True, "found": True, "entity": rec, "latency_ms": _ms(started)}


def _archived_entity(category: str, name: str) -> dict[str, Any] | None:
    """Fall back to the ARCHIVE tier when the active set has no record.

    Archiving moves the row out of `entities`, so without this a banned
    agent's next session would get found=false — which the firewall reads as
    'first time we have seen this agent' and starts back at the default
    trust of 50. Retiring an agent from the working set would silently
    un-ban it, which is the exact opposite of what archiving is for. The
    archived row carries the same trust body, so the ban survives.
    """
    path = Path(DB_PATH).expanduser()
    if not path.exists():
        return None
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        row = con.execute(
            "select body, archived_at, archive_reason from archived_entities "
            "where tenant_id = ? and category = ? and name = ? "
            "order by archived_at desc limit 1",
            (TENANT_ID, category, name),
        ).fetchone()
    except sqlite3.Error:
        return None
    finally:
        con.close()
    if row is None or row[0] is None:
        return None
    return {
        "category": category,
        "name": name,
        "body": json.loads(row[0]),
        "status": "archived",
        "archived_at": row[1],
        "archive_reason": row[2],
    }


@app.post("/search")
def search(q: SearchQuery) -> dict[str, Any]:
    """Full-text recall across tiers via FTS5. No embeddings involved."""
    started = time.perf_counter()
    c = client()
    if q.category:
        hits = c.search_entities(q.query, limit=q.limit, category=q.category)
    else:
        tiers = tuple(q.tiers) if q.tiers else None
        hits = c.search(q.query, limit=q.limit, tiers=tiers)
    return {"ok": True, "count": len(hits), "hits": hits, "latency_ms": _ms(started)}


@app.post("/set_state")
def set_state(b: StateBody) -> dict[str, Any]:
    """HOT tier. Per-session scratch, rewritten in place every tool call."""
    started = time.perf_counter()
    client().set_state(b.key, b.body)
    return {"ok": True, "latency_ms": _ms(started)}


@app.get("/get_state")
def get_state(key: str) -> dict[str, Any]:
    started = time.perf_counter()
    body = client().get_state(key)
    return {"ok": True, "found": body is not None, "body": body, "latency_ms": _ms(started)}


@app.post("/write_event")
def write_event(b: EventBody) -> dict[str, Any]:
    """COLD tier. Append one immutable decision to the journal."""
    started = time.perf_counter()
    event_id = client().write_event(
        acted=b.acted, evaluated=b.evaluated, forward=b.forward, extra=b.extra
    )
    return {"ok": True, "event_id": event_id, "latency_ms": _ms(started)}


@app.get("/read_events")
def read_events(limit: int = 50, since: str | None = None) -> dict[str, Any]:
    started = time.perf_counter()
    events = client().read_events(limit=limit, since=since)
    return {"ok": True, "count": len(events), "events": events, "latency_ms": _ms(started)}


@app.post("/set_reference")
def set_reference(b: ReferenceBody) -> dict[str, Any]:
    """REFERENCE tier. Static runbook / policy text.

    Note the SDK takes body as a string with a separate metadata dict, not
    a dict body — structured rules go in metadata.
    """
    started = time.perf_counter()
    client().set_reference(b.key, b.body, metadata=b.metadata)
    return {"ok": True, "latency_ms": _ms(started)}


@app.get("/get_reference")
def get_reference(key: str) -> dict[str, Any]:
    started = time.perf_counter()
    body = client().get_reference(key)
    return {"ok": True, "found": body is not None, "reference": body, "latency_ms": _ms(started)}


@app.post("/archive")
def archive(b: ArchiveBody) -> dict[str, Any]:
    """ARCHIVE tier. Retire an entity below the trust floor.

    Archival moves the row to archived_entities rather than deleting it:
    an agent banned for repeated violations must leave a record behind, or
    the ban itself becomes unauditable.
    """
    started = time.perf_counter()
    rec = client().archive_entity(b.category, b.name, b.reason)
    return {"ok": True, "archived": rec, "latency_ms": _ms(started)}


@app.get("/list_entities")
def list_entities(category: str | None = None, status: str | None = None, limit: int = 100):
    started = time.perf_counter()
    rows = client().list_entities(category, status=status, limit=limit)
    return {"ok": True, "count": len(rows), "entities": rows, "latency_ms": _ms(started)}


@app.get("/stats")
def stats() -> dict[str, Any]:
    """Tier counts for the dashboard's HOT/WARM/COLD panel."""
    c = client()
    entities = c.list_entities(limit=10000)
    events = c.read_events(limit=10000)
    by_category: dict[str, int] = {}
    for e in entities:
        by_category[e.get("category", "?")] = by_category.get(e.get("category", "?"), 0) + 1
    path = Path(DB_PATH).expanduser()
    return {
        "ok": True,
        "warm_entities": len(entities),
        "warm_by_category": by_category,
        "cold_events": len(events),
        "archived_entities": _archived_count(),
        "db_bytes": path.stat().st_size if path.exists() else 0,
        "vectors": 0,
    }


def _archived_count() -> int:
    """Rows in the ARCHIVE tier.

    Read straight from SQLite because the SDK exposes archive_entity() but
    no way to list what it archived, and a dashboard that shows four of five
    tiers is how the ARCHIVE tier stayed broken without anyone noticing.
    Read-only, on the same file the client already has open.
    """
    path = Path(DB_PATH).expanduser()
    if not path.exists():
        return 0
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        return con.execute(
            "select count(*) from archived_entities where tenant_id = ?", (TENANT_ID,)
        ).fetchone()[0]
    except sqlite3.Error:
        # The table only exists once the SDK has initialised the schema.
        return 0
    finally:
        con.close()


def _ms(started: float) -> float:
    return round((time.perf_counter() - started) * 1000, 3)


if __name__ == "__main__":
    import uvicorn

    # Render injects PORT and routes external traffic to it.
    # Fall back to SIBYL_PORT (Docker dev) and then 8787.
    port = int(
        os.environ.get("PORT")
        or os.environ.get("SIBYL_PORT", "8787")
    )
    uvicorn.run(
        app,
        host=os.environ.get("SIBYL_HOST", "0.0.0.0"),
        port=port,
    )
