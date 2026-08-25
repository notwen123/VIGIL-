"""Sibyl Memory service — VIGIL's cross-session trust memory.

Rewritten to use PostgreSQL (Supabase) instead of SQLite/sibyl_memory_client.
Same HTTP API, same five-tier schema — no disk, no card, no new accounts.
Set SIBYL_DATABASE_URL (or DATABASE_URL) to your Supabase direct connection string.

Five tiers:
  sibyl_entities/  WARM      durable agent/tool/policy trust records
  sibyl_state/     HOT       per-session scratch, rewritten in place
  sibyl_journal/   COLD      append-only decision log
  sibyl_reference/ REFERENCE static runbooks and policy text
  sibyl_archive/   ARCHIVE   entities retired below the trust floor
"""

from __future__ import annotations

import json
import os
import time
from typing import Any

import psycopg2
import psycopg2.extras
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

DATABASE_URL = os.environ.get("SIBYL_DATABASE_URL") or os.environ.get("DATABASE_URL")
TENANT_ID = os.environ.get("SIBYL_TENANT_ID", "00000000-0000-0000-0000-000000000001")

app = FastAPI(
    title="Sibyl Memory (VIGIL)",
    description="Cross-session trust memory for VIGIL — PostgreSQL backend.",
    version="2.0.0",
)

_conn = None


def db():
    global _conn
    if _conn is None or _conn.closed:
        if not DATABASE_URL:
            raise RuntimeError("SIBYL_DATABASE_URL or DATABASE_URL not set")
        _conn = psycopg2.connect(DATABASE_URL, cursor_factory=psycopg2.extras.RealDictCursor)
        _conn.autocommit = False
        _ensure_schema(_conn)
    return _conn


def _ensure_schema(conn) -> None:
    """Create all five tier tables if they don't exist. Idempotent."""
    with conn.cursor() as cur:
        cur.execute("""
        CREATE TABLE IF NOT EXISTS sibyl_entities (
            id          BIGSERIAL PRIMARY KEY,
            tenant_id   TEXT NOT NULL,
            category    TEXT NOT NULL,
            name        TEXT NOT NULL,
            body        JSONB NOT NULL DEFAULT '{}',
            status      TEXT,
            created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
            updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
            UNIQUE (tenant_id, category, name)
        );
        CREATE TABLE IF NOT EXISTS sibyl_state (
            id          BIGSERIAL PRIMARY KEY,
            tenant_id   TEXT NOT NULL,
            key         TEXT NOT NULL,
            body        JSONB NOT NULL DEFAULT '{}',
            updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
            UNIQUE (tenant_id, key)
        );
        CREATE TABLE IF NOT EXISTS sibyl_journal (
            id          BIGSERIAL PRIMARY KEY,
            tenant_id   TEXT NOT NULL,
            acted       TEXT[],
            evaluated   TEXT[],
            forward     TEXT[],
            extra       JSONB,
            created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
        );
        CREATE TABLE IF NOT EXISTS sibyl_reference (
            id          BIGSERIAL PRIMARY KEY,
            tenant_id   TEXT NOT NULL,
            key         TEXT NOT NULL,
            body        TEXT NOT NULL,
            metadata    JSONB,
            updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
            UNIQUE (tenant_id, key)
        );
        CREATE TABLE IF NOT EXISTS sibyl_archive (
            id             BIGSERIAL PRIMARY KEY,
            tenant_id      TEXT NOT NULL,
            category       TEXT NOT NULL,
            name           TEXT NOT NULL,
            body           JSONB NOT NULL DEFAULT '{}',
            archive_reason TEXT,
            archived_at    TIMESTAMPTZ NOT NULL DEFAULT now()
        );
        """)
    conn.commit()


def _to_list(v):
    if v is None:
        return None
    return [v] if isinstance(v, str) else list(v)


def _ms(started: float) -> float:
    return round((time.perf_counter() - started) * 1000, 3)


# ── request bodies ─────────────────────────────────────────────────────────────

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
    tiers: list[str] | None = None


class StateBody(BaseModel):
    key: str
    body: dict[str, Any] | list[Any]


class EventBody(BaseModel):
    acted: list[str] | str | None = None
    evaluated: list[str] | str | None = None
    forward: list[str] | str | None = None
    extra: dict[str, Any] | None = None


class ReferenceBody(BaseModel):
    key: str
    body: str
    metadata: dict[str, Any] | None = None


class ArchiveBody(BaseModel):
    category: str
    name: str
    reason: str | None = None


# ── endpoints ──────────────────────────────────────────────────────────────────

@app.get("/health")
def health() -> dict[str, Any]:
    try:
        conn = db()
        with conn.cursor() as cur:
            cur.execute("SELECT COUNT(*) AS c FROM sibyl_entities WHERE tenant_id = %s", (TENANT_ID,))
            row = cur.fetchone()
        return {
            "status": "ok",
            "backend": "postgresql",
            "vectors": False,
            "tenant_id": TENANT_ID,
            "warm_entities": row["c"] if row else 0,
            "db_exists": True,
        }
    except Exception as exc:
        return {"status": "error", "error": str(exc)}


@app.post("/remember")
def remember(b: RememberBody) -> dict[str, Any]:
    """WARM tier. Upsert a durable entity (agent trust, tool risk, policy)."""
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO sibyl_entities (tenant_id, category, name, body, status, updated_at)
            VALUES (%s, %s, %s, %s, %s, now())
            ON CONFLICT (tenant_id, category, name)
            DO UPDATE SET body = EXCLUDED.body, status = EXCLUDED.status, updated_at = now()
            RETURNING *
            """,
            (TENANT_ID, b.category, b.name, json.dumps(b.body), b.status),
        )
        row = dict(cur.fetchone())
    conn.commit()
    return {"ok": True, "entity": row, "latency_ms": _ms(started)}


@app.post("/recall")
def recall(q: RecallQuery) -> dict[str, Any]:
    """WARM tier. Point-lookup one entity — falls back to ARCHIVE.

    A miss is 'first time we have seen this agent'. Conflating 'unknown' with
    'banned' is how an amnesia bug silently becomes a permissive firewall.
    """
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute(
            "SELECT * FROM sibyl_entities WHERE tenant_id=%s AND category=%s AND name=%s",
            (TENANT_ID, q.category, q.name),
        )
        row = cur.fetchone()

    if row:
        return {"ok": True, "found": True, "entity": dict(row), "latency_ms": _ms(started)}

    # Fall back to ARCHIVE — a banned agent must not look new after archival.
    with conn.cursor() as cur:
        cur.execute(
            """SELECT * FROM sibyl_archive
               WHERE tenant_id=%s AND category=%s AND name=%s
               ORDER BY archived_at DESC LIMIT 1""",
            (TENANT_ID, q.category, q.name),
        )
        arc = cur.fetchone()

    if arc:
        rec = dict(arc)
        rec["status"] = "archived"
        return {"ok": True, "found": True, "entity": rec, "latency_ms": _ms(started)}

    return {"ok": True, "found": False, "entity": None, "latency_ms": _ms(started)}


@app.post("/archive")
def archive(b: ArchiveBody) -> dict[str, Any]:
    """ARCHIVE tier. Retire an entity below the trust floor."""
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute(
            "SELECT body FROM sibyl_entities WHERE tenant_id=%s AND category=%s AND name=%s",
            (TENANT_ID, b.category, b.name),
        )
        row = cur.fetchone()
        if not row:
            raise HTTPException(status_code=404, detail="entity not found")
        cur.execute(
            """INSERT INTO sibyl_archive (tenant_id, category, name, body, archive_reason)
               VALUES (%s, %s, %s, %s, %s) RETURNING *""",
            (TENANT_ID, b.category, b.name, json.dumps(row["body"]), b.reason),
        )
        archived = dict(cur.fetchone())
        cur.execute(
            "DELETE FROM sibyl_entities WHERE tenant_id=%s AND category=%s AND name=%s",
            (TENANT_ID, b.category, b.name),
        )
    conn.commit()
    return {"ok": True, "archived": archived, "latency_ms": _ms(started)}


@app.post("/set_state")
def set_state(b: StateBody) -> dict[str, Any]:
    """HOT tier. Per-session scratch, rewritten in place every tool call."""
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute(
            """INSERT INTO sibyl_state (tenant_id, key, body, updated_at)
               VALUES (%s, %s, %s, now())
               ON CONFLICT (tenant_id, key)
               DO UPDATE SET body = EXCLUDED.body, updated_at = now()""",
            (TENANT_ID, b.key, json.dumps(b.body)),
        )
    conn.commit()
    return {"ok": True, "latency_ms": _ms(started)}


@app.get("/get_state")
def get_state(key: str) -> dict[str, Any]:
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute("SELECT body FROM sibyl_state WHERE tenant_id=%s AND key=%s", (TENANT_ID, key))
        row = cur.fetchone()
    body = row["body"] if row else None
    return {"ok": True, "found": body is not None, "body": body, "latency_ms": _ms(started)}


@app.post("/write_event")
def write_event(b: EventBody) -> dict[str, Any]:
    """COLD tier. Append one immutable decision to the journal."""
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute(
            """INSERT INTO sibyl_journal (tenant_id, acted, evaluated, forward, extra)
               VALUES (%s, %s, %s, %s, %s) RETURNING id""",
            (TENANT_ID, _to_list(b.acted), _to_list(b.evaluated), _to_list(b.forward),
             json.dumps(b.extra) if b.extra else None),
        )
        event_id = cur.fetchone()["id"]
    conn.commit()
    return {"ok": True, "event_id": event_id, "latency_ms": _ms(started)}


@app.get("/read_events")
def read_events(limit: int = 50, since: str | None = None) -> dict[str, Any]:
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        if since:
            cur.execute(
                "SELECT * FROM sibyl_journal WHERE tenant_id=%s AND created_at > %s ORDER BY id DESC LIMIT %s",
                (TENANT_ID, since, limit),
            )
        else:
            cur.execute(
                "SELECT * FROM sibyl_journal WHERE tenant_id=%s ORDER BY id DESC LIMIT %s",
                (TENANT_ID, limit),
            )
        events = [dict(r) for r in cur.fetchall()]
    return {"ok": True, "count": len(events), "events": events, "latency_ms": _ms(started)}


@app.post("/set_reference")
def set_reference(b: ReferenceBody) -> dict[str, Any]:
    """REFERENCE tier. Static runbook / policy text."""
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute(
            """INSERT INTO sibyl_reference (tenant_id, key, body, metadata, updated_at)
               VALUES (%s, %s, %s, %s, now())
               ON CONFLICT (tenant_id, key)
               DO UPDATE SET body = EXCLUDED.body, metadata = EXCLUDED.metadata, updated_at = now()""",
            (TENANT_ID, b.key, b.body, json.dumps(b.metadata) if b.metadata else None),
        )
    conn.commit()
    return {"ok": True, "latency_ms": _ms(started)}


@app.get("/get_reference")
def get_reference(key: str) -> dict[str, Any]:
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute(
            "SELECT body, metadata FROM sibyl_reference WHERE tenant_id=%s AND key=%s",
            (TENANT_ID, key),
        )
        row = cur.fetchone()
    return {"ok": True, "found": row is not None, "reference": dict(row) if row else None, "latency_ms": _ms(started)}


@app.post("/search")
def search(q: SearchQuery) -> dict[str, Any]:
    """Full-text search via PostgreSQL tsvector — no embeddings."""
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        if q.category:
            cur.execute(
                """SELECT * FROM sibyl_entities WHERE tenant_id=%s AND category=%s
                   AND to_tsvector('english', body::text) @@ plainto_tsquery('english', %s)
                   LIMIT %s""",
                (TENANT_ID, q.category, q.query, q.limit),
            )
        else:
            cur.execute(
                """SELECT * FROM sibyl_entities WHERE tenant_id=%s
                   AND to_tsvector('english', body::text) @@ plainto_tsquery('english', %s)
                   LIMIT %s""",
                (TENANT_ID, q.query, q.limit),
            )
        hits = [dict(r) for r in cur.fetchall()]
    return {"ok": True, "count": len(hits), "hits": hits, "latency_ms": _ms(started)}


@app.get("/list_entities")
def list_entities(category: str | None = None, status: str | None = None, limit: int = 100):
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        if category and status:
            cur.execute("SELECT * FROM sibyl_entities WHERE tenant_id=%s AND category=%s AND status=%s LIMIT %s",
                        (TENANT_ID, category, status, limit))
        elif category:
            cur.execute("SELECT * FROM sibyl_entities WHERE tenant_id=%s AND category=%s LIMIT %s",
                        (TENANT_ID, category, limit))
        elif status:
            cur.execute("SELECT * FROM sibyl_entities WHERE tenant_id=%s AND status=%s LIMIT %s",
                        (TENANT_ID, status, limit))
        else:
            cur.execute("SELECT * FROM sibyl_entities WHERE tenant_id=%s LIMIT %s", (TENANT_ID, limit))
        rows = [dict(r) for r in cur.fetchall()]
    return {"ok": True, "count": len(rows), "entities": rows, "latency_ms": _ms(started)}


@app.get("/stats")
def stats() -> dict[str, Any]:
    """Tier counts for the dashboard HOT/WARM/COLD panel."""
    conn = db()
    with conn.cursor() as cur:
        cur.execute("SELECT COUNT(*) AS c FROM sibyl_entities WHERE tenant_id=%s", (TENANT_ID,))
        warm = cur.fetchone()["c"]
        cur.execute("SELECT category, COUNT(*) AS c FROM sibyl_entities WHERE tenant_id=%s GROUP BY category", (TENANT_ID,))
        by_cat = {r["category"]: r["c"] for r in cur.fetchall()}
        cur.execute("SELECT COUNT(*) AS c FROM sibyl_journal WHERE tenant_id=%s", (TENANT_ID,))
        cold = cur.fetchone()["c"]
        cur.execute("SELECT COUNT(*) AS c FROM sibyl_archive WHERE tenant_id=%s", (TENANT_ID,))
        archived = cur.fetchone()["c"]
    return {
        "ok": True,
        "warm_entities": warm,
        "warm_by_category": by_cat,
        "cold_events": cold,
        "archived_entities": archived,
        "backend": "postgresql",
        "vectors": 0,
        "db_bytes": 0,
    }


if __name__ == "__main__":
    import uvicorn

    port = int(os.environ.get("PORT") or os.environ.get("SIBYL_PORT", "8787"))
    uvicorn.run(app, host=os.environ.get("SIBYL_HOST", "0.0.0.0"), port=port)
