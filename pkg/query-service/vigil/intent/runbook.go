// Package intent holds VIGIL's REFERENCE tier: the standing rules an
// operator writes once and expects the firewall to still know about in six
// months.
//
// The distinction from the WARM tier is worth being precise about, because
// they look similar and behave very differently. WARM entities are
// *derived* — an agent's trust score is a consequence of things it did, and
// the firewall rewrites it without being asked. REFERENCE entries are
// *declared* — a human wrote "customer data cannot be shared" and nothing
// in the runtime may quietly revise it. Storing them in the same database
// but a different tier keeps that boundary legible: no code path in VIGIL
// writes REFERENCE except an explicit operator action.
//
// These are also the entries that give a block an explanation a person can
// act on. "Trust 12, blocked" tells an operator what happened; "trust 12,
// and runbook no_exfil says customer data cannot be shared" tells them why
// it was a rule in the first place.
package intent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
)

// Runbook is one declared rule.
type Runbook struct {
	Key  string `json:"key"`
	Rule string `json:"rule"`
	// Severity guides what a match should do, but does not itself enforce:
	// enforcement stays in the firewall, so a runbook edit can never widen
	// what the firewall is willing to allow.
	Severity string `json:"severity,omitempty"`
	Owner    string `json:"owner,omitempty"`
}

// Store persists runbooks to the REFERENCE tier.
type Store struct {
	sibyl  *sibyl.Client
	logger *slog.Logger
}

func NewStore(c *sibyl.Client, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{sibyl: c, logger: logger}
}

// Defaults are the runbooks VIGIL seeds on first start.
//
// Seeded rather than hardcoded into the decision path: an operator can edit
// or remove them, and the firewall reads whatever is there. Hardcoding them
// would make the REFERENCE tier decorative — the rules would live in Go and
// the memory copy would be a stale duplicate.
var Defaults = []Runbook{
	{
		Key:      "runbook_pii",
		Rule:     "Customer data cannot be shared outside the organization's network boundary.",
		Severity: "critical",
		Owner:    "security",
	},
	{
		Key:      "runbook_supply_chain",
		Rule:     "Packages that are typosquats of popular libraries must never be installed. A near-miss on a well-known name is treated as hostile, not as a typo.",
		Severity: "critical",
		Owner:    "platform",
	},
	{
		Key:      "runbook_repeat_offender",
		Rule:     "An agent with three or more recorded violations is blocked in all future sessions until a human restores its trust. Restarting the agent does not clear its history.",
		Severity: "high",
		Owner:    "security",
	},
}

// Seed writes the default runbooks if the memory layer is available.
func (s *Store) Seed(ctx context.Context) error {
	if s == nil || !s.sibyl.Configured() {
		return fmt.Errorf("intent: memory layer unavailable, runbooks not seeded")
	}
	for _, rb := range Defaults {
		if err := s.Put(ctx, rb); err != nil {
			return fmt.Errorf("intent: seeding %s: %w", rb.Key, err)
		}
	}
	s.logger.InfoContext(ctx, "vigil: REFERENCE runbooks seeded into cross-session memory",
		slog.Int("count", len(Defaults)))
	return nil
}

// Put stores or replaces one runbook.
func (s *Store) Put(ctx context.Context, rb Runbook) error {
	if s == nil || !s.sibyl.Configured() {
		return fmt.Errorf("intent: memory layer unavailable")
	}
	// The rule text is the body (the SDK's reference tier takes a string);
	// the structured fields ride in metadata so they stay queryable without
	// having to parse the prose back out.
	return s.sibyl.SetReference(ctx, rb.Key, rb.Rule, map[string]any{
		"severity": rb.Severity,
		"owner":    rb.Owner,
		"key":      rb.Key,
	})
}

// Get reads one runbook back.
func (s *Store) Get(ctx context.Context, key string) (map[string]any, bool, error) {
	if s == nil || !s.sibyl.Configured() {
		return nil, false, fmt.Errorf("intent: memory layer unavailable")
	}
	return s.sibyl.GetReference(ctx, key)
}
