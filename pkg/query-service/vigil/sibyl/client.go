// Package sibyl is VIGIL's cross-session memory: the layer that makes the
// firewall remember an agent between process restarts.
//
// Everything else in VIGIL is per-process. Session state, behavioural span
// history, the decision ring — all of it dies with the binary. That is the
// gap this package closes: an agent that got blocked for `pip install
// reqeusts` yesterday is still known to be a repeat typosquatter today,
// without re-deriving it and without paying an LLM to re-derive it.
//
// Why this sits ahead of HydraDB and Featherless in the enforcement order:
// a recall here is a local SQLite point lookup over an indexed unique key,
// measured at ~1-2ms including cold process open, costing nothing and
// requiring no network. HydraDB's graph-context queries measure 375ms-1s;
// a Featherless judgement costs money and seconds. Consulting the cheapest
// authoritative source first is not an optimisation, it is the difference
// between a firewall you can afford to run on every call and one you
// cannot.
//
// Failure posture is deliberate and load-bearing. See TrustScore: when the
// memory service is unreachable, trust cannot be computed, and this
// package refuses to guess. A missing memory layer must break the trust
// claim loudly rather than silently degrade into "every agent looks new
// and therefore trustworthy", which is precisely the failure that makes an
// amnesiac firewall worse than no firewall — it looks like it is working.
package sibyl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

// ErrUnavailable means the memory service could not be reached or refused
// the request. Callers must treat this as "trust is unknown", never as
// "trust is fine".
var ErrUnavailable = errors.New("sibyl: memory service unavailable")

// ErrNotConfigured means no memory service address is set at all. This is
// distinct from ErrUnavailable: it is a deployment that never intended to
// have memory, versus one whose memory is down.
var ErrNotConfigured = errors.New("sibyl: memory service not configured")

// Trust thresholds. These are the progressive-enforcement ladder, and each
// one is only meaningful because the score behind it survives a restart.
const (
	// TrustBanned - at or below this, the agent is blocked outright on
	// every future call, in every future session, on every device sharing
	// the database. This is the third-strike outcome.
	TrustBanned = 20
	// TrustArchive - below this the entity is archived out of the active
	// working set (it stays in archived_entities for audit).
	TrustArchive = 10
	// TrustACPAllow - ACP counterparties at or above this are served.
	TrustACPAllow = 70
	// TrustACPBlock - ACP counterparties below this are refused.
	TrustACPBlock = 30
	// TrustDefault - an agent we have never seen. Deliberately not 100:
	// an unknown agent is unproven, not proven good.
	TrustDefault = 50
)

// Client talks to the local Sibyl memory service over HTTP.
type Client struct {
	baseURL string
	http    *http.Client
	logger  *slog.Logger
}

// New builds a client against an explicit address.
func New(baseURL string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		baseURL: baseURL,
		// Short timeout on purpose: this is a local process reading a local
		// file. If it has not answered in two seconds it is not slow, it is
		// broken, and the caller needs to know that rather than wait.
		http:   &http.Client{Timeout: 2 * time.Second},
		logger: logger,
	}
}

// NewFromEnv builds a client from VIGIL_SIBYL_URL, defaulting to the local
// sidecar. Returns nil when memory is explicitly disabled, which every
// method treats as ErrNotConfigured rather than panicking.
func NewFromEnv(logger *slog.Logger) *Client {
	if os.Getenv("VIGIL_SIBYL_DISABLED") == "1" {
		return nil
	}
	url := os.Getenv("VIGIL_SIBYL_URL")
	if url == "" {
		url = "http://127.0.0.1:8787"
	}
	return New(url, logger)
}

// Configured reports whether a memory service is wired up. Safe on nil.
func (c *Client) Configured() bool { return c != nil }

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	if c == nil {
		return ErrNotConfigured
	}
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnavailable, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s returned %d", ErrUnavailable, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// --- WARM: durable entities -------------------------------------------------

// AgentTrust is what VIGIL remembers about one agent across all sessions.
type AgentTrust struct {
	TrustScore        int      `json:"trust_score"`
	TotalBlocks       int      `json:"total_blocks"`
	BannedTools       []string `json:"banned_tools"`
	LastViolationType string   `json:"last_violation_type,omitempty"`
	LastViolationTime string   `json:"last_violation_time,omitempty"`
	// BannedUntil carries the 24h second-strike ban as RFC3339. Empty when
	// the agent is not under a timed ban.
	BannedUntil string `json:"banned_until,omitempty"`
}

// Banned reports whether tool is currently denied to this agent, either by
// a standing ban entry or because the trust score has fallen to the floor.
func (a AgentTrust) Banned(tool string) bool {
	if a.TrustScore <= TrustBanned {
		return true
	}
	for _, t := range a.BannedTools {
		if t != tool {
			continue
		}
		if a.BannedUntil == "" {
			return true
		}
		// A timed ban that has expired stops applying, but the trust score
		// it came from does not reset — the history is still counted.
		if until, err := time.Parse(time.RFC3339, a.BannedUntil); err == nil {
			return time.Now().Before(until)
		}
		return true
	}
	return false
}

type entityEnvelope struct {
	OK    bool `json:"ok"`
	Found bool `json:"found"`
	Ent   struct {
		Body json.RawMessage `json:"body"`
	} `json:"entity"`
	LatencyMS float64 `json:"latency_ms"`
}

// TrustScore recalls an agent's cross-session trust.
//
// This is the function the deletion test targets. It returns an error
// rather than a default when memory is unavailable, and every caller in
// the firewall propagates that error rather than substituting a neutral
// score. Remove this package and the progressive-enforcement path cannot
// produce a verdict at all — which is the point: the claim "VIGIL
// remembers bad agents" is false without it, and the code says so instead
// of quietly allowing the call.
//
// found=false is not an error: it is a genuine first sighting, and the
// caller applies TrustDefault to it explicitly.
func (c *Client) TrustScore(ctx context.Context, agentID string) (trust AgentTrust, found bool, err error) {
	var env entityEnvelope
	if err := c.do(ctx, http.MethodPost, "/recall",
		map[string]string{"category": "agent", "name": agentID}, &env); err != nil {
		return AgentTrust{}, false, err
	}
	if !env.Found {
		return AgentTrust{TrustScore: TrustDefault}, false, nil
	}
	if err := json.Unmarshal(env.Ent.Body, &trust); err != nil {
		return AgentTrust{}, false, fmt.Errorf("sibyl: malformed trust record for %s: %w", agentID, err)
	}
	return trust, true, nil
}

// Remember upserts a durable entity (WARM tier).
func (c *Client) Remember(ctx context.Context, category, name string, body any) error {
	return c.do(ctx, http.MethodPost, "/remember", map[string]any{
		"category": category, "name": name, "body": body,
	}, nil)
}

// RememberAgent persists an agent's trust record.
func (c *Client) RememberAgent(ctx context.Context, agentID string, t AgentTrust) error {
	return c.Remember(ctx, "agent", agentID, t)
}

// Search runs FTS5 recall across the memory tiers.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]map[string]any, error) {
	var out struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := c.do(ctx, http.MethodPost, "/search",
		map[string]any{"query": query, "limit": limit}, &out); err != nil {
		return nil, err
	}
	return out.Hits, nil
}

// --- HOT: per-session state -------------------------------------------------

// SessionState is the live scratch record, rewritten in place every call.
type SessionState struct {
	BudgetLeft            float64 `json:"budget_left"`
	Intent                string  `json:"intent"`
	ViolationsThisSession int     `json:"violations_this_session"`
	LastTool              string  `json:"last_tool"`
	UpdatedAt             string  `json:"updated_at"`
}

// SetSessionState rewrites the HOT record for a session.
func (c *Client) SetSessionState(ctx context.Context, sessionID string, s SessionState) error {
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return c.do(ctx, http.MethodPost, "/set_state", map[string]any{
		"key": "session:" + sessionID, "body": s,
	}, nil)
}

// GetSessionState reads the HOT record back.
func (c *Client) GetSessionState(ctx context.Context, sessionID string) (SessionState, bool, error) {
	var out struct {
		Found bool            `json:"found"`
		Body  json.RawMessage `json:"body"`
	}
	if err := c.do(ctx, http.MethodGet,
		"/get_state?key=session:"+sessionID, nil, &out); err != nil {
		return SessionState{}, false, err
	}
	if !out.Found {
		return SessionState{}, false, nil
	}
	var s SessionState
	if err := json.Unmarshal(out.Body, &s); err != nil {
		return SessionState{}, false, err
	}
	return s, true, nil
}

// --- COLD: append-only journal ----------------------------------------------

// WriteDecision appends one firewall decision to the journal.
//
// Called for every ALLOW, BLOCK and PAUSE, not only for blocks: a journal
// that records refusals alone cannot answer "what did this agent get away
// with", which is the question that matters after an incident.
func (c *Client) WriteDecision(ctx context.Context, decision, tool, reason, agentID, sessionID, decisionHash string) (string, error) {
	var out struct {
		EventID string `json:"event_id"`
	}
	err := c.do(ctx, http.MethodPost, "/write_event", map[string]any{
		"acted": []string{decision + " " + tool},
		"extra": map[string]any{
			"reason":        reason,
			"agent_id":      agentID,
			"session_id":    sessionID,
			"decision_hash": decisionHash,
			"timestamp":     time.Now().UTC().Format(time.RFC3339Nano),
		},
	}, &out)
	return out.EventID, err
}

// ReadEvents returns recent journal entries, newest first.
func (c *Client) ReadEvents(ctx context.Context, limit int) ([]map[string]any, error) {
	var out struct {
		Events []map[string]any `json:"events"`
	}
	err := c.do(ctx, http.MethodGet, "/read_events?limit="+strconv.Itoa(limit), nil, &out)
	return out.Events, err
}

// --- REFERENCE and ARCHIVE --------------------------------------------------

// SetReference stores static runbook or policy text.
func (c *Client) SetReference(ctx context.Context, key, body string, metadata map[string]any) error {
	return c.do(ctx, http.MethodPost, "/set_reference", map[string]any{
		"key": key, "body": body, "metadata": metadata,
	}, nil)
}

// GetReference reads a runbook entry back.
func (c *Client) GetReference(ctx context.Context, key string) (map[string]any, bool, error) {
	var out struct {
		Found bool           `json:"found"`
		Ref   map[string]any `json:"reference"`
	}
	err := c.do(ctx, http.MethodGet, "/get_reference?key="+key, nil, &out)
	return out.Ref, out.Found, err
}

// Archive retires an entity below the trust floor, preserving it for audit.
func (c *Client) Archive(ctx context.Context, category, name, reason string) error {
	return c.do(ctx, http.MethodPost, "/archive", map[string]any{
		"category": category, "name": name, "reason": reason,
	}, nil)
}

// Stats returns tier counts for the dashboard.
func (c *Client) Stats(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodGet, "/stats", nil, &out)
	return out, err
}

// Health reports whether the memory service is answering and which
// database file it has open.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodGet, "/health", nil, &out)
	return out, err
}
