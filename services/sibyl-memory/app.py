"""Sibyl Memory service — VIGIL's cross-session trust memory.

One HTTP API and one five-tier schema over two interchangeable backends:

  PostgreSQL   when SIBYL_DATABASE_URL (or DATABASE_URL) is set.
               This is the hosted deployment. Render's free tier has an
               ephemeral filesystem, so a SQLite file there would be erased
               on every restart and redeploy — which for a service whose
               entire claim is "memory survives a restart" would be fatal.
               Supabase gives durable storage with no disk to pay for.

  SQLite       otherwise, at SIBYL_DB_PATH. This is what runs on a laptop
               with no account, no network and no credentials, and it is
               what makes ./demo/memory_demo.sh and the deletion test
               reproducible by anyone who clones this repository.

Both are load-bearing. Neither is a fallback for the other.

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
from pathlib import Path
from typing import Any

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

DATABASE_URL = os.environ.get("SIBYL_DATABASE_URL") or os.environ.get("DATABASE_URL")
DB_PATH = os.environ.get("SIBYL_DB_PATH", "~/.sibyl-memory/memory.db")
TENANT_ID = os.environ.get("SIBYL_TENANT_ID", "00000000-0000-0000-0000-000000000001")

# Two backends, one schema, chosen by whether a Postgres URL is present.
#
# Postgres is the hosted deployment (Supabase, no disk to manage). SQLite is
# what runs on a laptop with no account and no network, which is not a
# convenience — it is what makes `./demo/memory_demo.sh` and the deletion test
# reproducible by anyone who clones this repository. When the service became
# Postgres-only, that demo stopped starting at all, and a proof nobody else
# can run is not a proof.
#
# The difference is confined to this block. All twenty-five call sites below
# are written once, in psycopg2's dialect, and the SQLite side adapts.
SQLITE = not DATABASE_URL
BACKEND = "sqlite" if SQLITE else "postgresql"

if SQLITE:
    import sqlite3
else:
    import psycopg2
    import psycopg2.extras

app = FastAPI(
    title="Sibyl Memory (VIGIL)",
    description=(
        "Cross-session trust memory for VIGIL. "
        "SQLite on local disk, PostgreSQL when SIBYL_DATABASE_URL is set."
    ),
    version="2.1.0",
)

_conn = None


class _SqliteCursor:
    """A psycopg2-shaped cursor over sqlite3.

    Three differences to paper over, and only three: the parameter marker is
    `?` rather than `%s`, rows are tuples rather than mappings, and a cursor
    is not a context manager. Everything else in the SQL — ON CONFLICT DO
    UPDATE, RETURNING — SQLite has supported since 3.35.
    """

    def __init__(self, cur): self._cur = cur
    def __enter__(self): return self
    def __exit__(self, *exc): self._cur.close(); return False

    def execute(self, sql, params=()):
        # %s -> ?, and now() -> CURRENT_TIMESTAMP. Both are Postgres
        # spellings the call sites use; neither exists in SQLite.
        sql = sql.replace("%s", "?").replace("now()", "CURRENT_TIMESTAMP")
        # psycopg2 adapts a Python list into a Postgres TEXT[]; sqlite3 refuses
        # to bind one at all ("type 'list' is not supported"). Encoding here —
        # and decoding again in _row — is what keeps the COLD journal writable
        # on both backends without touching the call sites.
        params = tuple(
            json.dumps(v) if isinstance(v, (list, dict)) else v for v in params
        )
        return self._cur.execute(sql, params)

    # Columns that are JSONB in Postgres, where psycopg2 hands back a parsed
    # object. SQLite stores them as TEXT, so without this the same endpoint
    # would return a dict on one backend and a JSON string on the other — and
    # the Go client, which unmarshals trust from `body`, would fail on one of
    # them. The wire format has to be identical or the backends are not
    # interchangeable.
    _JSON_COLS = ("body", "extra", "metadata", "acted", "evaluated", "forward")

    def _row(self, r):
        if r is None:
            return None
        d = dict(r)
        for col in self._JSON_COLS:
            v = d.get(col)
            if isinstance(v, str):
                try:
                    d[col] = json.loads(v)
                except (ValueError, TypeError):
                    pass  # a genuine string column, e.g. reference body
        return d

    def fetchone(self): return self._row(self._cur.fetchone())
    def fetchall(self): return [self._row(r) for r in self._cur.fetchall()]

    def __getattr__(self, name): return getattr(self._cur, name)


class _SqliteConn:
    """Wraps sqlite3.Connection so `with conn.cursor() as cur` works."""

    def __init__(self, raw): self._raw = raw
    @property
    def closed(self): return False
    def cursor(self): return _SqliteCursor(self._raw.cursor())
    def commit(self): self._raw.commit()
    def rollback(self): self._raw.rollback()


def db():
    global _conn
    if _conn is None or _conn.closed:
        if SQLITE:
            path = Path(DB_PATH).expanduser()
            path.parent.mkdir(parents=True, exist_ok=True)
            raw = sqlite3.connect(str(path), check_same_thread=False)
            raw.row_factory = sqlite3.Row
            # WAL keeps a reader from blocking the writer, which matters
            # because the firewall reads on the hot path while the same
            # process is journalling decisions behind it.
            raw.execute("PRAGMA journal_mode=WAL")
            _conn = _SqliteConn(raw)
        else:
            _conn = psycopg2.connect(
                DATABASE_URL, cursor_factory=psycopg2.extras.RealDictCursor
            )
            _conn.autocommit = False
        _ensure_schema(_conn)
    return _conn


def _ensure_schema(conn) -> None:
    """Create all five tier tables if they don't exist. Idempotent.

    The two dialects differ only in column types and the autoincrement
    spelling. SQLite has no JSONB and no array type, so both become TEXT
    holding JSON — which is what the code already writes, since every body is
    passed through json.dumps() before it reaches a placeholder.
    """
    if SQLITE:
        ddl = """
        CREATE TABLE IF NOT EXISTS sibyl_entities (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            tenant_id   TEXT NOT NULL,
            category    TEXT NOT NULL,
            name        TEXT NOT NULL,
            body        TEXT NOT NULL DEFAULT '{}',
            status      TEXT,
            created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
            updated_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
            UNIQUE (tenant_id, category, name)
        );
        CREATE TABLE IF NOT EXISTS sibyl_state (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            tenant_id   TEXT NOT NULL,
            key         TEXT NOT NULL,
            body        TEXT NOT NULL DEFAULT '{}',
            updated_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
            UNIQUE (tenant_id, key)
        );
        CREATE TABLE IF NOT EXISTS sibyl_journal (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            tenant_id   TEXT NOT NULL,
            acted       TEXT,
            evaluated   TEXT,
            forward     TEXT,
            extra       TEXT,
            created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
        );
        CREATE TABLE IF NOT EXISTS sibyl_reference (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            tenant_id   TEXT NOT NULL,
            key         TEXT NOT NULL,
            body        TEXT NOT NULL,
            metadata    TEXT,
            updated_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
            UNIQUE (tenant_id, key)
        );
        CREATE TABLE IF NOT EXISTS sibyl_archive (
            id             INTEGER PRIMARY KEY AUTOINCREMENT,
            tenant_id      TEXT NOT NULL,
            category       TEXT NOT NULL,
            name           TEXT NOT NULL,
            body           TEXT NOT NULL DEFAULT '{}',
            archive_reason TEXT,
            archived_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
        );
        """
        raw = conn._raw
        raw.executescript(ddl)
        conn.commit()
        return

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
            "backend": BACKEND,
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


@app.post("/forget")
def forget(b: ArchiveBody) -> dict[str, Any]:
    """Erase an entity from the active set AND the archive.

    The only way to lift a ban. Without it a ban is permanent, because
    /recall falls back to the archive on purpose — archiving an agent must
    not un-ban it. That is correct for a sanction and wrong as the whole
    story: one false positive would blacklist a legitimate agent forever,
    with no operator action able to reverse it.

    Deliberately separate from /archive, and deliberately not called by the
    firewall. Nothing in the enforcement path forgets anything on its own —
    forgetting is an operator decision, and it leaves the COLD journal
    intact, so the record that the agent *was* banned survives even though
    the ban itself does not.
    """
    started = time.perf_counter()
    conn = db()
    with conn.cursor() as cur:
        cur.execute(
            "DELETE FROM sibyl_entities WHERE tenant_id=%s AND category=%s AND name=%s",
            (TENANT_ID, b.category, b.name),
        )
        live = cur.rowcount
        cur.execute(
            "DELETE FROM sibyl_archive WHERE tenant_id=%s AND category=%s AND name=%s",
            (TENANT_ID, b.category, b.name),
        )
        archived = cur.rowcount
    conn.commit()
    return {
        "ok": True,
        "category": b.category,
        "name": b.name,
        "removed": {"active": live, "archived": archived},
        "note": "journal entries are kept; only the trust record is gone",
        "latency_ms": _ms(started),
    }


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
        "backend": BACKEND,
        "vectors": 0,
        "db_bytes": 0,
    }


if __name__ == "__main__":
    import uvicorn

    port = int(os.environ.get("PORT") or os.environ.get("SIBYL_PORT", "8787"))
    uvicorn.run(app, host=os.environ.get("SIBYL_HOST", "0.0.0.0"), port=port)
