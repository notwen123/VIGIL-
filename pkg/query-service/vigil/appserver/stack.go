package appserver

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/SigNoz/signoz/pkg/acp"
	"github.com/SigNoz/signoz/pkg/query-service/vigil"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/audit"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/cost"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine/plugins"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/intent"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/recovery"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/recovery/actions"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/state"
)

// stack holds the governance components. Construction lives here rather than in
// NewServer, which is already a 900-line function.
type stack struct {
	fw       *firewall.Firewall
	policies *policy.Store
	gov      *engine.GovernanceEngine
	heal     *recovery.SelfHealingEngine
	router   *llm.Router
	ledger   *audit.Ledger
	hydra    *hydra.Client
	// sibyl is the cross-session memory layer — the one dependency whose
	// absence changes what the product can claim to do.
	sibyl    *sibyl.Client
	anchorer *audit.Anchorer
	acp      *acp.Service
	x402     *cost.Rail
}

// buildStack assembles the governance pipeline.
func buildStack(logger *slog.Logger, budgetLimit float64) *stack {
	ctx := context.Background()

	// --- Recovery actions ---------------------------------------------------
	heal := recovery.NewSelfHealingEngine(logger)
	heal.RegisterAction(actions.NewKillAgentAction())
	heal.RegisterAction(actions.NewFallbackModelAction())
	heal.RegisterAction(actions.NewRetryAction())
	heal.RegisterAction(actions.NewReduceContextAction())
	heal.RegisterAction(actions.NewSwitchPromptAction())
	heal.RegisterAction(actions.NewDisableToolAction())
	heal.RegisterAction(actions.NewCircuitBreakerAction())
	heal.RegisterAction(actions.NewEscalateHumanAction())
	heal.RegisterAction(actions.NewAlertAction(broadcastAlert))

	// --- Governance engine ---------------------------------------------------
	// This closure is the single line that makes the recovery actions live: the
	// engine already calls its hook per violation, it simply never had one.
	gov := engine.NewGovernanceEngine(logger, func(ctx context.Context, agentCtx *engine.AgentContext, v engine.RuleResult) {
		heal.ExecuteRecovery(ctx, agentCtx, v)
	})

	// Only the detectors that can actually fire on MCP data are registered.
	//
	// TokenExplosion, RepeatedPrompt, and PromptRecursion read InputTokens,
	// OutputTokens, and PromptText. An MCP *tool* call carries none of those,
	// because Vigil intercepts tool calls, not the agent's LLM turns.
	// Registering them would be dead code presented as coverage, and the
	// governance endpoint would report nine active rules when six can fire.
	// They become registerable when an SDK-side span-ingest path exists.
	gov.RegisterPlugin(plugins.NewInfiniteLoopDetector(envInt("LOOP_MAX_REPEATS", 5)))
	gov.RegisterPlugin(plugins.NewBudgetExceededDetector())
	gov.RegisterPlugin(plugins.NewRetryStormDetector(envInt("RETRY_STORM_MAX", 3)))
	gov.RegisterPlugin(plugins.NewLatencySpikeDetector(envDuration("LATENCY_SPIKE", 30*time.Second)))
	gov.RegisterPlugin(plugins.NewToolTimeoutDetector(envDuration("TOOL_TIMEOUT", 60*time.Second)))
	gov.RegisterPlugin(plugins.NewAgentStuckDetector(envDuration("AGENT_STUCK", 120*time.Second)))

	// --- Inference -----------------------------------------------------------
	var provider llm.Provider = llm.DeterministicProvider{}
	if chain := llm.ChainFromEnv(logger); chain == nil {
		// Info, not warn: running without inference is a supported
		// configuration, not a degraded one. Deterministic checks still govern.
		logger.InfoContext(ctx, "vigil: no inference credentials, running deterministic-only")
	} else {
		provider = chain
		logger.InfoContext(ctx, "vigil: inference configured",
			slog.String("provider", chain.Name()),
			slog.Any("roles", chain.ConfiguredRoles()),
		)
		// In the background: startup must not block on a vendor's network. The
		// firewall works from the first request either way — an unprobed vendor
		// is simply one that has not been verified yet.
		go func() {
			pctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			chain.Probe(pctx)
		}()
	}
	router := llm.NewRouter(logger, provider)

	// --- Audit ledger --------------------------------------------------------
	var ledger *audit.Ledger
	ledgerPath := vigil.EnvOr("AUDIT_PATH", audit.DefaultPath)
	if l, err := audit.Open(ledgerPath); err != nil {
		// Error-level, but not fatal. Losing the audit record is bad; wedging
		// every agent in the deployment because of it is worse.
		logger.ErrorContext(ctx, "vigil: could not open audit ledger, decisions will not be recorded",
			slog.String("path", ledgerPath),
			slog.String("error", err.Error()),
		)
	} else {
		ledger = l
		logger.InfoContext(ctx, "vigil: audit ledger open",
			slog.String("path", ledgerPath),
			slog.Int("existing_events", l.Len()),
		)
	}

	// --- HydraDB graph layer ---------------------------------------------------
	// nil when unconfigured — every hydra.Client method is a safe no-op on a
	// nil receiver (see hydra.go), so the firewall needs no separate branch for
	// "no HydraDB credential" the way it already doesn't for "no Featherless
	// credential". Database provisioning happens in the background: it is
	// setup latency, not request latency, and must never block the server
	// from accepting its first call.
	hydraClient := hydra.NewFromEnv(logger)
	if hydraClient == nil {
		logger.InfoContext(ctx, "vigil: no HydraDB credential, running without the graph layer")
	} else {
		logger.InfoContext(ctx, "vigil: HydraDB configured, provisioning database in the background")
		go func() {
			pctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			if err := hydraClient.EnsureDatabase(pctx); err != nil {
				logger.ErrorContext(ctx, "vigil: HydraDB database did not become ready", slog.String("error", err.Error()))
				return
			}
			logger.InfoContext(ctx, "vigil: HydraDB database ready")
		}()
	}

	policies := policy.NewStore()
	fc := firewall.DefaultForecaster()
	if v := vigil.Env("SOFT_LIMIT_PCT"); v != "" {
		if p, err := strconv.ParseFloat(v, 64); err == nil && p > 0 && p <= 1 {
			fc.SoftLimitPct = p
		}
	}

	// Cross-session memory. Unlike the graph and the model chain this is not
	// a nice-to-have: without it the progressive-enforcement ladder has no
	// past to reason about and every violation is a first violation forever.
	mem := sibyl.NewFromEnv(logger)
	anchorer := audit.NewAnchorerFromEnv(logger)
	acpSvc := acp.New(mem, logger)

	fw := firewall.New(firewall.Deps{
		Logger:      logger,
		Policies:    policies,
		Gov:         gov,
		Heal:        heal,
		Router:      router,
		Ledger:      ledger,
		Forecaster:  fc,
		Hydra:       hydraClient,
		Compromised: firewall.NewCompromisedList(),
		Sibyl:       mem,
		Anchorer:    anchorer,
	})

	// Probe memory at startup and say plainly whether cross-session
	// enforcement is actually on. "Configured" is a claim about a config
	// file; this is a claim about the service answering. Getting that wrong
	// is the dangerous direction — an operator who believes repeat offenders
	// are being stopped when they are not is worse off than one who knows
	// they are not.
	memoryLive := false
	if mem.Configured() {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if h, err := mem.Health(probeCtx); err != nil {
			logger.ErrorContext(ctx,
				"vigil: trust_score unavailable — cross-session enforcement is DISABLED, repeat offenders will NOT be blocked",
				slog.String("error", err.Error()),
				slog.String("fix", "start services/sibyl-memory (see MEMORY.md)"))
		} else {
			memoryLive = true
			logger.InfoContext(ctx, "vigil: cross-session memory live",
				slog.Any("db", h["db_path"]), slog.Any("bytes", h["db_bytes"]))
			// Seed the REFERENCE tier. Idempotent: an upsert per runbook.
			if err := intent.NewStore(mem, logger).Seed(ctx); err != nil {
				logger.WarnContext(ctx, "vigil: runbook seeding failed", slog.String("error", err.Error()))
			}
		}
		cancel()
	} else {
		logger.ErrorContext(ctx,
			"vigil: memory layer disabled (VIGIL_SIBYL_DISABLED=1) — cross-session enforcement is OFF")
	}

	logger.InfoContext(ctx, "vigil: governance engine active",
		slog.Any("plugins", gov.Plugins()),
		slog.Float64("budget_limit", budgetLimit),
		slog.Bool("inference", router.Available()),
		slog.Bool("audit", ledger != nil),
		slog.Bool("hydra", hydraClient.Configured()),
		slog.Bool("memory", memoryLive),
		slog.Bool("base_anchoring", anchorer.Enabled()),
	)

	return &stack{
		fw: fw, policies: policies, gov: gov, heal: heal, router: router,
		ledger: ledger, hydra: hydraClient,
		sibyl: mem, anchorer: anchorer, acp: acpSvc, x402: cost.NewRailFromEnv(),
	}
}

// broadcastAlert pushes a governance alert to the dashboard over the existing
// WebSocket hub. Slack and webhook delivery are configured separately by the
// integrations package; this is the always-available floor.
func broadcastAlert(ctx context.Context, agentCtx *engine.AgentContext, v engine.RuleResult) error {
	state.GetHub().BroadcastMessage(map[string]any{
		"type":      "VIGIL_ALERT",
		"rule":      v.RuleName,
		"severity":  string(v.Severity),
		"reason":    v.Reason,
		"action":    string(v.AutomaticAction),
		"agent_id":  agentCtx.TraceID,
		"timestamp": time.Now(),
	})
	return nil
}

func envInt(name string, def int) int {
	if v := vigil.Env(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v := vigil.Env(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
