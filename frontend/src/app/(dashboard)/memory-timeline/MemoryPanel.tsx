'use client'

import { useEffect, useState } from 'react'
import { Database, ShieldAlert, ShieldCheck, Link2, Cpu, AlertTriangle } from 'lucide-react'

// The cross-session memory panel.
//
// The single most important thing on this screen is the enforcing/not
// enforcing banner. "Memory configured" and "memory answering" are different
// claims, and an operator who believes repeat offenders are being blocked
// when they are not is worse off than one who knows they are not — so a
// memory outage is rendered as a loud red state, never as a quiet zero.

interface MemoryHealth {
  enforcing: boolean
  db_path?: string
  db_bytes?: number
  backend?: string
  vectors?: boolean
  reason?: string
  impact?: string
  fix?: string
}

interface AgentTrust {
  trust_score: number
  total_blocks: number
  banned_tools?: string[]
  last_violation_type?: string
  last_violation_time?: string
  banned_until?: string
}

interface AgentRecall {
  agent_id: string
  found: boolean
  trust: AgentTrust
  recall_ms: number
  source: string
  llm_calls: number
}

interface Stats {
  warm_entities: number
  warm_by_category: Record<string, number>
  cold_events: number
  db_bytes: number
  vectors: number
}

interface AcpJob {
  job_id: string
  buyer_agent_id: string
  verdict: 'ALLOW' | 'BLOCK' | 'REVIEW'
  reason: string
  trust_score: number
  recall_ms: number
  decided_at: string
}

interface BaseStatus {
  anchoring_enabled: boolean
  wallet?: string
  chain_id?: number
  anchors_sent: number
  receipts?: { tx_hash?: string; explorer_url?: string; sent: boolean; hash: string; reason?: string }[]
  x402?: Record<string, unknown>
  note?: string
}

const j = async (url: string) => {
  const r = await fetch(url, { cache: 'no-store' })
  return { ok: r.ok, body: await r.json().catch(() => ({})) }
}

export function MemoryPanel() {
  const [health, setHealth] = useState<MemoryHealth | null>(null)
  const [stats, setStats] = useState<Stats | null>(null)
  const [jobs, setJobs] = useState<AcpJob[]>([])
  const [base, setBase] = useState<BaseStatus | null>(null)
  const [agentId, setAgentId] = useState('trading-agent-alpha')
  const [recall, setRecall] = useState<AgentRecall | null>(null)
  const [recallErr, setRecallErr] = useState<string | null>(null)

  // Consecutive failed health polls. A single miss is not an outage: both
  // services are on a free tier that sleeps, so a cold start returns 502 and
  // a burst of polls can earn a 429 — neither of which means enforcement
  // stopped. Flipping the banner on the first failure cried wolf on a
  // perfectly healthy system, and an alarm that fires on nothing is an alarm
  // nobody reads when it matters.
  const [misses, setMisses] = useState(0)
  const OUTAGE_AFTER = 3

  const refresh = async () => {
    const h = await j('/api/v1/vigil/memory/health')
    if (h.ok) {
      setHealth(h.body)
      setMisses(0)
    } else {
      // Keep the last known good health rather than overwriting it with an
      // error body. The distinction the rest of this codebase draws between
      // "unreachable" and "unenforced" has to survive into the UI.
      setMisses((n) => n + 1)
    }
    const s = await j('/api/v1/vigil/memory/stats')
    if (s.ok) setStats(s.body)
    const a = await j('/api/v1/vigil/acp/jobs')
    if (a.ok) setJobs(a.body.jobs || [])
    const b = await j('/api/v1/vigil/base/status')
    if (b.ok) setBase(b.body)
  }

  const lookup = async (id: string) => {
    setRecallErr(null)
    const r = await j(`/api/v1/vigil/memory/agent?id=${encodeURIComponent(id)}`)
    if (!r.ok) {
      setRecall(null)
      setRecallErr(r.body?.error || 'trust_score unavailable')
      return false
    }
    setRecall(r.body)
    return true
  }

  useEffect(() => {
    refresh()
    // Retry the first lookup once. On a cold free-tier service the initial
    // recall can 502 or 429, and the error it leaves behind is sticky until
    // the operator clicks Recall — so the panel would sit there showing
    // "trust_score unavailable" about a service that had already come up.
    lookup(agentId).then((ok) => {
      if (!ok) setTimeout(() => lookup(agentId), 4000)
    })
    // 15s, not 5s. Four endpoints per tick against two free-tier services
    // was enough to earn a 429 on its own.
    const t = setInterval(refresh, 15000)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const enforcing = health?.enforcing === true
  // Unreachable is a third state, not a synonym for unenforced. Only call it
  // an outage once the service has missed several polls in a row.
  const unreachable = misses > 0 && misses < OUTAGE_AFTER
  const outage = misses >= OUTAGE_AFTER

  return (
    <div className="space-y-6">
      {/* The banner that must never be subtle. */}
      <div
        className={`card p-5 border-l-4 ${
          outage
            ? 'border-l-red-500 bg-red-50'
            : unreachable
              ? 'border-l-amber-500 bg-amber-50'
              : enforcing
                ? 'border-l-green-500'
                : 'border-l-red-500 bg-red-50'
        }`}
      >
        <div className="flex items-start gap-3">
          {enforcing && !outage ? (
            <ShieldCheck
              className={`w-5 h-5 mt-0.5 shrink-0 ${unreachable ? 'text-amber-600' : 'text-green-600'}`}
            />
          ) : (
            <ShieldAlert className="w-5 h-5 text-red-600 mt-0.5 shrink-0" />
          )}
          <div className="flex-1">
            <h2
              className={`text-sm font-semibold ${
                outage ? 'text-red-800' : unreachable ? 'text-amber-800' : enforcing ? 'text-gray-900' : 'text-red-800'
              }`}
            >
              {outage
                ? 'Cross-session enforcement is OFF — repeat offenders will NOT be blocked'
                : unreachable
                  ? `Reconnecting to the memory service — ${misses} missed check${misses > 1 ? 's' : ''}`
                  : enforcing
                    ? 'Cross-session enforcement is ON'
                    : 'Cross-session enforcement is OFF — repeat offenders will NOT be blocked'}
            </h2>
            {unreachable && !outage ? (
              <p className="text-xs text-amber-700 mt-1">
                Showing the last confirmed state. Enforcement is unchanged — the dashboard just
                could not reach the service on this poll. It is reported as an outage after{' '}
                {OUTAGE_AFTER} consecutive misses.
              </p>
            ) : enforcing ? (
              <p className="text-xs text-gray-500 mt-1 font-mono">
                {health?.backend}
                {health?.db_path ? ` · ${health.db_path}` : ''} ·{' '}
                {(health?.db_bytes ?? 0).toLocaleString()} bytes · vectors: {String(health?.vectors)}
              </p>
            ) : (
              <>
                <p className="text-xs text-red-700 mt-1">{health?.reason || health?.impact}</p>
                {health?.fix && <p className="text-xs text-red-600 mt-1 font-mono">{health.fix}</p>}
              </>
            )}
          </div>
        </div>
      </div>

      {/* Tier counts */}
      <div className="grid grid-cols-4 gap-4">
        <div className="stat-card">
          <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">WARM entities</p>
          <p className="text-xl font-bold mt-1 text-gray-900">{stats?.warm_entities ?? '—'}</p>
          <p className="text-[10px] text-gray-400 mt-1">agents · tools · policies</p>
        </div>
        <div className="stat-card">
          <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">COLD journal</p>
          <p className="text-xl font-bold mt-1 text-gray-900">{stats?.cold_events ?? '—'}</p>
          <p className="text-[10px] text-gray-400 mt-1">decisions recorded</p>
        </div>
        <div className="stat-card">
          <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">On disk</p>
          <p className="text-xl font-bold mt-1 text-gray-900">
            {stats ? `${Math.round(stats.db_bytes / 1024)}KB` : '—'}
          </p>
          <p className="text-[10px] text-gray-400 mt-1">
            {health?.backend === 'postgresql' ? 'PostgreSQL' : 'single SQLite file'}
          </p>
        </div>
        <div className="stat-card">
          <p className="text-[11px] text-gray-500 font-medium uppercase tracking-wider">Vectors</p>
          <p className="text-xl font-bold mt-1 text-green-600">{stats?.vectors ?? 0}</p>
          <p className="text-[10px] text-gray-400 mt-1">zero embeddings</p>
        </div>
      </div>

      {/* Trust lookup — the blame view */}
      <div className="card p-5">
        <div className="flex items-center gap-2 mb-4">
          <Database className="w-4 h-4 text-orange-600" />
          <h2 className="text-sm font-semibold text-gray-800">Agent trust — recalled from memory</h2>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            lookup(agentId)
          }}
          className="flex gap-2 mb-4"
        >
          <input
            value={agentId}
            onChange={(e) => setAgentId(e.target.value)}
            placeholder="agent id"
            className="flex-1 px-3 py-2 text-sm border border-gray-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-orange-200"
          />
          <button type="submit" className="btn-orange">Recall</button>
        </form>

        {recallErr && (
          <div className="px-4 py-3 rounded-xl bg-red-50 border border-red-200 text-red-700 text-sm flex items-center gap-2">
            <AlertTriangle className="w-4 h-4" />
            {recallErr}
          </div>
        )}

        {recall && !recallErr && (
          recall.found ? (
            <div>
              <div className="flex items-baseline gap-3 mb-3">
                <span
                  className={`text-3xl font-bold ${
                    recall.trust.trust_score <= 20
                      ? 'text-red-600'
                      : recall.trust.trust_score < 50
                        ? 'text-orange-500'
                        : 'text-green-600'
                  }`}
                >
                  {recall.trust.trust_score}
                </span>
                <span className="text-xs text-gray-500">
                  trust · {recall.trust.total_blocks} violation(s) ·
                  recalled in {recall.recall_ms}ms · {recall.llm_calls} LLM calls
                </span>
              </div>

              {/* Trust bar: 100 -> 0 */}
              <div className="progress-track h-2 mb-3">
                <div
                  className={`h-full rounded-full ${
                    recall.trust.trust_score <= 20 ? 'bg-red-500' : 'bg-orange-400'
                  }`}
                  style={{ width: `${Math.max(2, recall.trust.trust_score)}%` }}
                />
              </div>

              <dl className="text-xs space-y-1">
                {recall.trust.banned_tools?.length ? (
                  <div className="flex gap-2">
                    <dt className="text-gray-500 w-28">Banned tools</dt>
                    <dd className="font-mono text-red-700">{recall.trust.banned_tools.join(', ')}</dd>
                  </div>
                ) : null}
                {recall.trust.banned_until && (
                  <div className="flex gap-2">
                    <dt className="text-gray-500 w-28">Ban expires</dt>
                    <dd className="font-mono text-gray-700">{recall.trust.banned_until}</dd>
                  </div>
                )}
                {recall.trust.last_violation_time && (
                  <div className="flex gap-2">
                    <dt className="text-gray-500 w-28">Last violation</dt>
                    <dd className="font-mono text-gray-700">
                      {recall.trust.last_violation_type} @ {recall.trust.last_violation_time}
                    </dd>
                  </div>
                )}
                <div className="flex gap-2">
                  <dt className="text-gray-500 w-28">Source</dt>
                  <dd className="font-mono text-gray-500">{recall.source}</dd>
                </div>
              </dl>
            </div>
          ) : (
            <p className="text-sm text-gray-500">
              No record for <span className="font-mono">{recall.agent_id}</span> — first sighting.
              Unknown is not the same as trusted.
            </p>
          )
        )}
      </div>

      {/* ACP jobs */}
      <div className="card overflow-hidden">
        <div className="px-6 py-4 border-b border-gray-100 flex items-center gap-2">
          <Cpu className="w-4 h-4 text-orange-600" />
          <h2 className="text-sm font-semibold text-gray-800">ACP jobs — decided from memory</h2>
        </div>
        <table className="data-table">
          <thead>
            <tr><th>Job</th><th>Counterparty</th><th>Trust</th><th>Verdict</th><th>Recall</th></tr>
          </thead>
          <tbody>
            {jobs.slice().reverse().map((jb) => (
              <tr key={jb.job_id}>
                <td className="font-mono text-xs">{jb.job_id}</td>
                <td className="font-mono text-xs">{jb.buyer_agent_id}</td>
                <td className="font-mono text-xs">{jb.trust_score}</td>
                <td>
                  <span className={`pill ${
                    jb.verdict === 'BLOCK' ? 'pill-red' : jb.verdict === 'ALLOW' ? 'pill-gray' : 'pill-orange'
                  }`}>{jb.verdict}</span>
                </td>
                <td className="font-mono text-xs text-gray-500">{jb.recall_ms}ms</td>
              </tr>
            ))}
            {jobs.length === 0 && (
              <tr><td colSpan={5} className="text-gray-400 text-sm py-8 text-center">No ACP jobs yet.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Base anchoring */}
      <div className="card p-5">
        <div className="flex items-center gap-2 mb-3">
          <Link2 className="w-4 h-4 text-orange-600" />
          <h2 className="text-sm font-semibold text-gray-800">Base — audit anchoring</h2>
        </div>
        {base?.anchoring_enabled ? (
          <>
            <p className="text-xs text-gray-500 mb-3 font-mono">
              wallet {base.wallet} · chain {base.chain_id} · {base.anchors_sent} anchored
            </p>
            <ul className="space-y-1">
              {(base.receipts || []).filter((r) => r.sent).slice(-8).reverse().map((r) => (
                <li key={r.tx_hash} className="text-xs font-mono">
                  <a href={r.explorer_url} target="_blank" rel="noreferrer" className="text-orange-700 hover:underline">
                    {r.tx_hash?.slice(0, 18)}…
                  </a>
                </li>
              ))}
            </ul>
          </>
        ) : (
          // Stated, not hidden. An unanchored ledger is a documented state,
          // and no transaction hash is shown because none was ever sent.
          <p className="text-xs text-gray-500">
            Not anchoring. {base?.note || 'Set VIGIL_BASE_PRIVATE_KEY to enable.'} No transaction
            hashes are displayed because none have been submitted.
          </p>
        )}
      </div>
    </div>
  )
}
