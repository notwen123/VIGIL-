// Package acp exposes VIGIL as a provider on the Agent Commerce Protocol.
//
// The premise: on an open agent marketplace, a buyer agent asks a seller
// agent to do something, and the seller has no way to tell a reputable
// counterparty from one that has been draining wallets all week. Reputation
// is exactly the problem VIGIL's memory layer already solves internally —
// this package points it outward.
//
// An arriving ACP job carries a buyer agent id. VIGIL recalls that id from
// the same local trust store the firewall uses (one SQLite file, ~1ms, no
// inference), and answers:
//
//	trust >= 70   ALLOW   serve the job
//	trust <  30   BLOCK   refuse, citing the recalled history
//	otherwise     REVIEW  neither trusted nor condemned
//
// The reason a refusal can cite specifics — "trust 12, three prior
// violations, last was a typosquat" — is that the memory outlived every
// session in which those violations happened. A stateless provider would
// have to either trust everyone or trust no one.
//
// On the chain-facing half: registering as a provider and settling jobs on
// Base mainnet requires a funded signer. This package implements job
// evaluation and the provider lifecycle, and refuses to invent an
// on-chain identity it does not have — see Registered(). With no signer
// configured it runs in local evaluation mode, which is honest and still
// exercises the decision path that matters.
package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
)

// Verdict is VIGIL's answer to an ACP job.
type Verdict string

const (
	VerdictAllow  Verdict = "ALLOW"
	VerdictBlock  Verdict = "BLOCK"
	VerdictReview Verdict = "REVIEW"
)

// Job is one inbound ACP request.
type Job struct {
	JobID         string `json:"job_id"`
	BuyerAgentID  string `json:"buyer_agent_id"`
	RequestedTool string `json:"requested_tool"`
	Intent        string `json:"intent"`
}

// Decision is the answer returned to the ACP network, and the record kept
// for the dashboard.
type Decision struct {
	JobID        string  `json:"job_id"`
	BuyerAgentID string  `json:"buyer_agent_id"`
	Verdict      Verdict `json:"verdict"`
	Reason       string  `json:"reason"`
	TrustScore   int     `json:"trust_score"`
	Recalled     bool    `json:"recalled"`
	PriorBlocks  int     `json:"prior_blocks"`
	RecallMS     float64 `json:"recall_ms"`
	DecidedAt    string  `json:"decided_at"`
	// Source names what produced the verdict. Always local memory here:
	// an ACP decision that required an LLM round-trip would be too slow and
	// too expensive to sit in a marketplace request path.
	Source string `json:"source"`
}

// Service evaluates ACP jobs against VIGIL's trust memory.
type Service struct {
	sibyl   *sibyl.Client
	logger  *slog.Logger
	chainID int64
	wallet  string

	mu      sync.Mutex
	history []Decision
}

// New builds the ACP service.
func New(c *sibyl.Client, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	chainID := int64(8453) // Base mainnet
	if v := os.Getenv("VIGIL_ACP_CHAIN_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			chainID = n
		}
	}
	return &Service{
		sibyl:   c,
		logger:  logger,
		chainID: chainID,
		wallet:  os.Getenv("VIGIL_ACP_WALLET_ADDRESS"),
	}
}

// Registered reports whether VIGIL has an on-chain provider identity.
//
// False means job evaluation still works locally but nothing has been
// registered on Base. Reporting this honestly matters: a provider that
// claims registration it does not have would have counterparties expecting
// on-chain settlement that cannot happen.
func (s *Service) Registered() bool {
	return s != nil && s.wallet != "" && os.Getenv("VIGIL_ACP_PRIVATE_KEY") != ""
}

// Status describes the provider for the dashboard and /vigil/acp/status.
func (s *Service) Status() map[string]any {
	if s == nil {
		return map[string]any{"enabled": false}
	}
	return map[string]any{
		"enabled":         true,
		"registered":      s.Registered(),
		"chain_id":        s.chainID,
		"wallet":          s.wallet,
		"memory_backed":   s.sibyl.Configured(),
		"allow_threshold": sibyl.TrustACPAllow,
		"block_threshold": sibyl.TrustACPBlock,
		"note": "Job evaluation is local and memory-backed. " +
			"On-chain registration requires VIGIL_ACP_PRIVATE_KEY and a funded wallet.",
		"jobs_handled": len(s.History()),
	}
}

// Evaluate decides one ACP job from recalled counterparty trust.
func (s *Service) Evaluate(ctx context.Context, job Job) (Decision, error) {
	started := time.Now()
	d := Decision{
		JobID:        job.JobID,
		BuyerAgentID: job.BuyerAgentID,
		DecidedAt:    time.Now().UTC().Format(time.RFC3339),
		Source:       "sibyl_memory(local sqlite, no llm)",
	}

	if !s.sibyl.Configured() {
		// Without memory there is no reputation to consult, and answering
		// ALLOW would be indistinguishable from having checked. Say so.
		d.Verdict = VerdictReview
		d.Reason = "counterparty trust unavailable: VIGIL memory layer is not configured, so no reputation could be recalled"
		s.record(d)
		return d, fmt.Errorf("acp: %s", d.Reason)
	}

	trust, found, err := s.sibyl.TrustScore(ctx, job.BuyerAgentID)
	if err != nil {
		d.Verdict = VerdictReview
		d.Reason = "counterparty trust unavailable: " + err.Error()
		s.record(d)
		return d, err
	}

	d.TrustScore = trust.TrustScore
	d.Recalled = found
	d.PriorBlocks = trust.TotalBlocks
	d.RecallMS = float64(time.Since(started).Microseconds()) / 1000

	switch {
	case !found:
		// Unknown counterparty. Not condemned, but not vouched for either —
		// defaulting an unknown agent to ALLOW is how a marketplace gets
		// farmed by fresh identities.
		d.Verdict = VerdictReview
		d.Reason = fmt.Sprintf("no prior record for %s; unknown counterparties are not auto-approved", job.BuyerAgentID)
	case trust.TrustScore < sibyl.TrustACPBlock:
		d.Verdict = VerdictBlock
		d.Reason = fmt.Sprintf(
			"refused from VIGIL memory: %s has trust %d after %d recorded violation(s)%s — recalled in %.2fms without an LLM call",
			job.BuyerAgentID, trust.TrustScore, trust.TotalBlocks,
			violationSuffix(trust), d.RecallMS)
	case trust.TrustScore >= sibyl.TrustACPAllow:
		d.Verdict = VerdictAllow
		d.Reason = fmt.Sprintf("counterparty %s has trust %d with %d violations on record",
			job.BuyerAgentID, trust.TrustScore, trust.TotalBlocks)
	default:
		d.Verdict = VerdictReview
		d.Reason = fmt.Sprintf("counterparty %s has trust %d, between the block (%d) and allow (%d) thresholds",
			job.BuyerAgentID, trust.TrustScore, sibyl.TrustACPBlock, sibyl.TrustACPAllow)
	}

	s.logger.InfoContext(ctx, "vigil: ACP job decided from cross-session memory",
		slog.String("job_id", job.JobID),
		slog.String("buyer", job.BuyerAgentID),
		slog.String("verdict", string(d.Verdict)),
		slog.Int("trust", d.TrustScore),
		slog.Float64("recall_ms", d.RecallMS))

	s.record(d)
	return d, nil
}

func violationSuffix(t sibyl.AgentTrust) string {
	if t.LastViolationType == "" {
		return ""
	}
	return ", most recently " + t.LastViolationType
}

func (s *Service) record(d Decision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, d)
	if len(s.history) > 100 {
		s.history = s.history[len(s.history)-100:]
	}
}

// History returns recent ACP decisions for the dashboard.
func (s *Service) History() []Decision {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Decision, len(s.history))
	copy(out, s.history)
	return out
}

// Handler serves the ACP job endpoint.
//
// POST /vigil/acp/job with a Job body. This is the surface an ACP node
// (or the demo harness) drives; the SDK's own transport can call it the
// same way.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var job Job
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			http.Error(w, `{"error":"invalid job body"}`, http.StatusBadRequest)
			return
		}
		if job.BuyerAgentID == "" {
			http.Error(w, `{"error":"buyer_agent_id is required"}`, http.StatusBadRequest)
			return
		}
		if job.JobID == "" {
			job.JobID = "job-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}

		d, err := s.Evaluate(r.Context(), job)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			// A REVIEW verdict caused by unavailable memory is a 503: the
			// caller should retry or escalate, not read it as approval.
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(d)
	}
}
