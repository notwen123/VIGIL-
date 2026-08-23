package cost

// HOT tier: the live per-session record, rewritten in place on every tool
// call.
//
// This is the only one of the five memory tiers that is *expected* to be
// lost. A session's remaining budget and last tool are scratch: they
// describe a conversation that ends when the terminal does. Keeping them
// in the memory store anyway buys two things that in-process state cannot:
//
//   - a session that is resumed (same session id, new process) picks up its
//     budget where it left off instead of silently receiving a fresh
//     allowance, which is the loophole an agent restart would otherwise open;
//   - the dashboard can show live sessions belonging to processes it is not
//     itself running.
//
// Rewritten in place rather than appended: set_state is an upsert on the
// key, so the row count stays flat no matter how long a session runs. The
// append-only history lives in the COLD journal, which is the tier designed
// to grow.

import (
	"context"
	"log/slog"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
)

// SessionMemory persists live session state to the memory layer.
type SessionMemory struct {
	sibyl  *sibyl.Client
	logger *slog.Logger
}

// NewSessionMemory wires the HOT tier. A nil client is safe: every method
// becomes a no-op, and the firewall's own deletion path (not this one)
// is what surfaces a missing memory layer.
func NewSessionMemory(c *sibyl.Client, logger *slog.Logger) *SessionMemory {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionMemory{sibyl: c, logger: logger}
}

// Record rewrites the HOT state for a session after a tool call.
//
// Deliberately best-effort. A failed HOT write must not fail the call: the
// authoritative budget accounting lives in the in-process tracker, and this
// is a projection of it for durability and display. Losing a projection is
// an inconvenience; refusing an agent's work because a projection could not
// be written is not a trade worth making.
func (m *SessionMemory) Record(ctx context.Context, sessionID string, st sibyl.SessionState) {
	if m == nil || !m.sibyl.Configured() {
		return
	}
	if err := m.sibyl.SetSessionState(ctx, sessionID, st); err != nil {
		m.logger.WarnContext(ctx, "vigil: HOT session state write failed",
			slog.String("session", sessionID), slog.String("error", err.Error()))
	}
}

// Resume reads a session's persisted budget back.
//
// Returns found=false for a genuinely new session, which the caller must
// distinguish from a zero budget — conflating them would either hand every
// new session no allowance at all, or hand a spent session a fresh one.
func (m *SessionMemory) Resume(ctx context.Context, sessionID string) (sibyl.SessionState, bool) {
	if m == nil || !m.sibyl.Configured() {
		return sibyl.SessionState{}, false
	}
	st, found, err := m.sibyl.GetSessionState(ctx, sessionID)
	if err != nil {
		m.logger.WarnContext(ctx, "vigil: HOT session state read failed",
			slog.String("session", sessionID), slog.String("error", err.Error()))
		return sibyl.SessionState{}, false
	}
	return st, found
}
