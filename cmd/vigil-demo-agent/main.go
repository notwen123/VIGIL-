// Command vigil-demo-agent drives the real VIGIL firewall from the command
// line, so the cross-session memory claim can be demonstrated with two
// genuinely separate processes rather than two function calls.
//
// This is not a simulator. It constructs the same firewall.Firewall the
// server does, with the same Sibyl client, and calls the same Check(). The
// only thing it stands in for is the MCP transport.
//
//	go run ./cmd/vigil-demo-agent -agent trading-agent-alpha \
//	    -tool run_command -arg 'pip install reqeusts' -repeat 3
//
// Set VIGIL_SIBYL_DISABLED=1 to sever the memory layer and watch the same
// call be allowed — that is the deletion test.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
)

func main() {
	var (
		sessionID = flag.String("session", "demo-session", "session id (ephemeral)")
		agentID   = flag.String("agent", "demo-agent", "agent id (durable, what trust is keyed on)")
		tool      = flag.String("tool", "run_command", "tool name")
		arg       = flag.String("arg", "pip install reqeusts", "command argument")
		repeat    = flag.Int("repeat", 1, "how many times to attempt the call")
		verbose   = flag.Bool("v", false, "log firewall internals")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if *verbose {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	// Deliberately permissive policy: network on, no tool allowlist, a
	// budget far above the call cost. The deterministic layer will happily
	// allow this, so anything that blocks can only have come from memory.
	store := policy.NewStore()
	p := &policy.Policy{
		SessionID: *sessionID, DeclaredIntent: "package management",
		NetworkAccess: true, BudgetUSD: 100,
	}
	p.Normalize()
	store.Set(p)

	mem := sibyl.NewFromEnv(logger)

	// The incident-response denylist, read from VIGIL_COMPROMISED_PACKAGES.
	//
	// This is what produces the *first* block, and the distinction matters
	// for understanding what memory does. Memory does not originate
	// enforcement — it records and then re-applies what some other stage
	// already decided. Something has to catch the typosquat the first time;
	// after that, memory is what stops the agent in every subsequent
	// session without re-deriving anything.
	compromised := firewall.NewCompromisedList()

	fw := firewall.New(firewall.Deps{
		Logger:      logger,
		Policies:    store,
		Sibyl:       mem,
		Compromised: compromised,
		// No Hydra and no Router on purpose: whatever this prints was
		// decided without a graph query and without an LLM call.
	})

	if mem == nil {
		fmt.Printf("    %-9s memory layer DISABLED (VIGIL_SIBYL_DISABLED=1)\n", "memory:")
	} else if h, err := mem.Health(context.Background()); err != nil {
		fmt.Printf("    %-9s unreachable: %v\n", "memory:", err)
	} else {
		fmt.Printf("    %-9s %v (%v bytes)\n", "memory:", h["db_path"], h["db_bytes"])
	}

	ctx := context.Background()
	for i := 1; i <= *repeat; i++ {
		res := fw.Check(ctx, firewall.Call{
			SessionID: *sessionID,
			AgentID:   *agentID,
			Tool:      *tool,
			Args:      map[string]any{"command": *arg},
			ToolCost:  0.01,
			Budget:    100,
		})

		mark := "·"
		switch res.Decision {
		case firewall.Block:
			mark = "\033[31m✖\033[0m"
		case firewall.Pause:
			mark = "\033[33m‖\033[0m"
		case firewall.Allow:
			mark = "\033[32m✔\033[0m"
		}

		trust := "n/a"
		recall := ""
		if res.Sibyl != nil {
			trust = fmt.Sprintf("%d", res.Sibyl.TrustScore)
			recall = fmt.Sprintf("  recall=%.2fms", res.Sibyl.RecallMS)
		}

		model := res.ModelUsed
		if model == "" {
			model = "none"
		}

		fmt.Printf("    %s attempt %d/%d  %-6s stage=%-14s trust=%-4s model=%s%s\n",
			mark, i, *repeat, res.Decision, res.Stage, trust, model, recall)

		if res.TrustUnavailable {
			fmt.Printf("      \033[31m^ trust_unavailable: this verdict is UNENFORCED, not clean\033[0m\n")
		}
		if res.Reason != "" {
			fmt.Printf("      %s\n", truncate(res.Reason, 150))
		}

		// The journal write is fire-and-forget; give it a beat so the next
		// process reliably observes it. Real deployments do not need this.
		time.Sleep(150 * time.Millisecond)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
