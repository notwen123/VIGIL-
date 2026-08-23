package firewall

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
)

// This file is the load-bearing memory path. If it is removed, VIGIL stops
// being able to answer the question the product is named for: "have I seen
// this agent misbehave before?"
//
// The progressive-enforcement ladder below is the concrete reason memory
// cannot be optional. Each rung is defined in terms of what happened in
// *previous sessions*, which no amount of in-process state can supply
// after a restart:
//
//	1st violation   PAUSE   — recorded, agent warned, trust -20
//	2nd violation   BLOCK   — same agent+tool again, 24h ban written to WARM
//	3rd violation   BLOCK   — trust below the floor, blocked in every future
//	                          session on every device sharing the database
//
// Strike two and strike three are only distinguishable from strike one by
// reading memory. Delete the recall and every violation is a first
// violation forever, which means the firewall degrades to a system that
// warns endlessly and never actually stops anybody.

// ErrTrustUnavailable is returned when the memory layer cannot supply a
// trust score. It is deliberately fatal to the enforcement decision rather
// than defaulted: an agent whose history is unknown must not be treated as
// an agent with a clean history.
var ErrTrustUnavailable = errors.New("trust_score unavailable: sibyl memory layer is required for progressive enforcement")

// StageSibyl names the memory stage in decision records.
const StageSibyl = "sibyl_memory"

// SibylReport is the memory evidence attached to a decision, so an
// operator can see *why* a fresh process blocked an agent it has, from its
// own runtime's point of view, never met.
type SibylReport struct {
	AgentID     string   `json:"agent_id"`
	TrustScore  int      `json:"trust_score"`
	TotalBlocks int      `json:"total_blocks"`
	BannedTools []string `json:"banned_tools,omitempty"`
	BannedUntil string   `json:"banned_until,omitempty"`
	Recalled    bool     `json:"recalled"`
	Strike      int      `json:"strike"`
	RecallMS    float64  `json:"recall_ms"`
	// Source records that this verdict came from local memory and not from
	// a graph query or a model call, which is the efficiency claim.
	Source string `json:"source"`
}

// checkSibylTrust is enforcement stage 1b: after the deterministic policy
// check, before any HydraDB query and long before any model call.
//
// Returning (true, report, nil) blocks the call outright. That block costs
// one local SQLite lookup — no graph traversal, no inference, no spend.
func (f *Firewall) checkSibylTrust(ctx context.Context, c Call) (blocked bool, pause bool, report *SibylReport, err error) {
	if !f.deps.Sibyl.Configured() {
		// Memory is not wired up at all. Enforcement that depends on it
		// cannot run, and saying so is the honest outcome.
		return false, false, nil, ErrTrustUnavailable
	}

	agentID := agentIDFor(c)
	start := time.Now()
	trust, found, err := f.deps.Sibyl.TrustScore(ctx, agentID)
	if err != nil {
		// The service is configured but not answering. This is exactly the
		// case where guessing is worst: we know memory is supposed to exist,
		// so an empty answer is a malfunction, not a clean record.
		return false, false, nil, fmt.Errorf("%w: %v", ErrTrustUnavailable, err)
	}

	rep := &SibylReport{
		AgentID:     agentID,
		TrustScore:  trust.TrustScore,
		TotalBlocks: trust.TotalBlocks,
		BannedTools: trust.BannedTools,
		BannedUntil: trust.BannedUntil,
		Recalled:    found,
		Strike:      trust.TotalBlocks,
		RecallMS:    float64(time.Since(start).Microseconds()) / 1000,
		Source:      "sibyl_memory(local sqlite, no llm)",
	}

	if !found {
		// First sighting. Nothing to enforce against yet.
		return false, false, rep, nil
	}

	// Strike three: trust has fallen to the floor. Blocked everywhere, in
	// every future session, until an operator intervenes.
	if trust.TrustScore <= sibyl.TrustBanned {
		return true, false, rep, nil
	}

	// Strike two: this exact tool is under a standing or timed ban.
	if trust.Banned(c.Tool) {
		return true, false, rep, nil
	}

	// Strike one: known offender, but not for this tool and not yet at the
	// floor. Pause for a human rather than block outright.
	if trust.TotalBlocks > 0 && trust.TrustScore < 50 {
		return false, true, rep, nil
	}

	return false, false, rep, nil
}

// recordSibylViolation walks the agent down the trust ladder after a block
// or pause, and persists the result so the *next* process starts from the
// new position rather than from zero.
//
// This is the write half of the load-bearing pair. Without it the read
// half always finds a clean record and the ladder never advances.
func (f *Firewall) recordSibylViolation(ctx context.Context, res Result) {
	if !f.deps.Sibyl.Configured() {
		return
	}
	agentID := res.AgentID
	if agentID == "" {
		agentID = "session:" + res.SessionID
	}

	trust, found, err := f.deps.Sibyl.TrustScore(ctx, agentID)
	if err != nil {
		f.deps.Logger.ErrorContext(ctx, "vigil: cannot record violation, memory unavailable",
			"agent", agentID, "error", err.Error())
		return
	}
	if !found {
		trust = sibyl.AgentTrust{TrustScore: sibyl.TrustDefault}
	}

	trust.TotalBlocks++
	// Each violation costs 20 points, so a clean agent (50) reaches the
	// ban floor on its third strike. The arithmetic is deliberately plain
	// enough that an operator can predict it.
	trust.TrustScore -= 20
	if trust.TrustScore < 0 {
		trust.TrustScore = 0
	}
	trust.LastViolationType = res.Stage
	trust.LastViolationTime = time.Now().UTC().Format(time.RFC3339)

	// Second strike on the same tool earns that tool a 24h ban, written to
	// WARM so it outlives this process.
	if trust.TotalBlocks >= 2 && !contains(trust.BannedTools, res.Tool) {
		trust.BannedTools = append(trust.BannedTools, res.Tool)
		trust.BannedUntil = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	}

	if err := f.deps.Sibyl.RememberAgent(ctx, agentID, trust); err != nil {
		f.deps.Logger.ErrorContext(ctx, "vigil: failed to persist agent trust",
			"agent", agentID, "error", err.Error())
		return
	}

	// Also record the tool's own typosquat/risk history, so a tool that
	// many agents abuse carries that reputation independently.
	if res.Decision == Block {
		_ = f.deps.Sibyl.Remember(ctx, "tool", res.Tool, map[string]any{
			"risk":             "high",
			"last_block_at":    trust.LastViolationTime,
			"last_block_stage": res.Stage,
		})
	}

	// Below the archive floor the agent leaves the active working set. The
	// record persists in archived_entities: a ban has to stay auditable.
	if trust.TrustScore < sibyl.TrustArchive {
		if err := f.deps.Sibyl.Archive(ctx, "agent", agentID,
			fmt.Sprintf("trust %d below floor %d after %d violations",
				trust.TrustScore, sibyl.TrustArchive, trust.TotalBlocks)); err != nil {
			f.deps.Logger.WarnContext(ctx, "vigil: archive failed", "agent", agentID, "error", err.Error())
		}
	}

	f.deps.Logger.InfoContext(ctx, "vigil: agent trust updated in cross-session memory",
		"agent", agentID, "trust", trust.TrustScore, "strikes", trust.TotalBlocks,
		"banned_tools", trust.BannedTools)
}

// agentIDFor resolves the identity trust is tracked against. Sessions are
// ephemeral by design; the agent identity is what must persist across
// them, so a session id is only a fallback for callers that supply no
// agent.
func agentIDFor(c Call) string {
	if v, ok := c.Args["agent_id"].(string); ok && v != "" {
		return v
	}
	if c.AgentID != "" {
		return c.AgentID
	}
	return "session:" + c.SessionID
}

// anchorToBase publishes this decision's ledger link to Base when a signer
// is configured. Fire-and-forget: an anchoring outage must never wedge a
// tool call, and the local hash chain remains authoritative either way.
func (f *Firewall) anchorToBase(ctx context.Context, res Result, prevHash, hash string) {
	if f.deps.Anchorer == nil || !f.deps.Anchorer.Enabled() {
		return
	}
	// Only blocks are anchored. Anchoring every allow would spend gas
	// proportional to ordinary traffic for no additional evidentiary value:
	// what a third party needs to verify later is that a refusal happened
	// and was not retroactively edited away.
	if res.Decision == Allow {
		return
	}
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if _, err := f.deps.Anchorer.Anchor(bg, hash, prevHash); err != nil {
			f.deps.Logger.WarnContext(bg, "vigil: base anchoring failed, local ledger unaffected",
				"error", err.Error())
		}
	}()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
