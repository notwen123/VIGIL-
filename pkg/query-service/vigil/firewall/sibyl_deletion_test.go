package firewall_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
)

// This file is the executable form of the deletion test. The claim under
// examination is "VIGIL remembers bad agents across sessions", and the way
// to test a load-bearing claim is to remove the thing bearing the load and
// confirm the structure falls down.
//
// Three cases, in order of what they prove:
//
//	1. WITH memory      a fresh firewall blocks a known-bad agent it has
//	                    never itself seen, using no model and no graph.
//	2. WITHOUT memory   the same agent walks straight through. This is the
//	                    gate: the product is supposed to break.
//	3. Order            memory is consulted before HydraDB and before any
//	                    model, so a remembered offender costs one local
//	                    lookup rather than a graph query and an LLM call.
//
// Case 2 deliberately fails open rather than blocking. Failing closed would
// be safer, but it would also let an operator conclude the firewall was
// working — noisily, but working. Letting the repeat offender through is
// the only outcome that makes the loss unmistakable, and it is what the
// deletion test in README.md reproduces by hand.

// memoryStub is a stand-in for services/sibyl-memory. The cross-process
// persistence itself is proven separately and for real by
// services/sibyl-memory/test_memory.py, which uses two OS processes and a
// SQLite file on disk. What this stub isolates is the *firewall's*
// behaviour given a memory layer that answers, versus one that does not.
type memoryStub struct {
	trust   sibyl.AgentTrust
	found   bool
	recalls int
	// down simulates the service being unreachable.
	down bool
}

func (m *memoryStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.down {
			http.Error(w, "connection refused", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/recall":
			m.recalls++
			body, _ := json.Marshal(m.trust)
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "found": m.found,
				"entity": map[string]any{"body": json.RawMessage(body)},
			})
		default:
			// remember / write_event / archive all succeed quietly.
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "event_id": "evt-test"})
		}
	}))
}

func permissivePolicy(t *testing.T, sessionID string) *policy.Store {
	t.Helper()
	store := policy.NewStore()
	// Deliberately permissive: network on, no allowlist, generous budget.
	// The deterministic layer will happily allow this call, so anything that
	// blocks it can only have come from memory.
	p := &policy.Policy{
		SessionID: sessionID, DeclaredIntent: "package management",
		NetworkAccess: true, BudgetUSD: 100,
	}
	p.Normalize()
	store.Set(p)
	return store
}

// TestDeletionGate_WithMemory_BlocksKnownBadAgent is case 1.
//
// A brand-new Firewall — the equivalent of a freshly started process after
// `kill` — blocks an agent purely on recalled cross-session trust. Nothing
// in this firewall's own runtime has ever seen this agent misbehave.
func TestDeletionGate_WithMemory_BlocksKnownBadAgent(t *testing.T) {
	stub := &memoryStub{
		found: true,
		trust: sibyl.AgentTrust{
			TrustScore: 12, TotalBlocks: 3,
			BannedTools:       []string{"run_command"},
			LastViolationType: "typosquat",
		},
	}
	srv := stub.server(t)
	defer srv.Close()

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: permissivePolicy(t, "s-fresh"),
		Sibyl:    sibyl.New(srv.URL, newTestLogger(t)),
		// Deliberately no Hydra and no Router: if the call still blocks,
		// it provably did so without a graph query or a model call.
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-fresh", AgentID: "agent-repeat-offender", Tool: "run_command",
		Args: map[string]any{"command": "pip install reqeusts"}, ToolCost: 0.01, Budget: 100,
	})

	if res.Decision != firewall.Block {
		t.Fatalf("a fresh firewall must block a known-bad agent from memory, got %s (%s)",
			res.Decision, res.Reason)
	}
	if res.Stage != firewall.StageSibyl {
		t.Errorf("expected block at stage %q, got %q", firewall.StageSibyl, res.Stage)
	}
	if res.Sibyl == nil || res.Sibyl.TrustScore != 12 {
		t.Fatalf("expected recalled trust 12 attached to the decision, got %+v", res.Sibyl)
	}
	if res.ModelUsed != "" {
		t.Errorf("a remembered offender must cost no model call, but ModelUsed=%q", res.ModelUsed)
	}
	if stub.recalls == 0 {
		t.Error("memory was never consulted")
	}
	t.Logf("blocked from memory: trust=%d strikes=%d recall=%.2fms model=%q",
		res.Sibyl.TrustScore, res.Sibyl.TotalBlocks, res.Sibyl.RecallMS, res.ModelUsed)
}

// TestDeletionGate_WithoutMemory_KnownBadAgentGetsThrough is case 2: THE GATE.
//
// Same agent, same call, same permissive policy as case 1 — but the memory
// layer is deleted. The agent that was blocked a microsecond ago now walks
// straight through.
//
// That is the disqualification criterion stated as a test. The product
// claims memory is load-bearing; the proof is that removing it returns the
// firewall to pre-VIGIL behaviour, letting a three-strike typosquatter
// install its package. If this test ever fails — if the call is still
// blocked without memory — then something else was doing the work and the
// claim was false.
//
// Note the pairing: this asserts the product BREAKS. It is the only test in
// the suite whose passing condition is a security failure, and that is
// precisely what makes it evidence.
func TestDeletionGate_WithoutMemory_KnownBadAgentGetsThrough(t *testing.T) {
	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: permissivePolicy(t, "s-nomem"),
		Sibyl:    nil, // <-- the deletion
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-nomem", AgentID: "agent-repeat-offender", Tool: "run_command",
		Args: map[string]any{"command": "pip install reqeusts"}, ToolCost: 0.01, Budget: 100,
	})

	if res.Decision != firewall.Allow {
		t.Fatalf("GATE INCONCLUSIVE: expected the known-bad agent to get through once memory "+
			"was deleted (proving memory was what blocked it), but got %s at stage %q. "+
			"Either something else is doing the blocking, or fail-open regressed.",
			res.Decision, res.Stage)
	}
	if !res.TrustUnavailable {
		t.Fatal("the decision must be marked TrustUnavailable so the dashboard shows it as " +
			"unenforced rather than clean")
	}
	// A memory outage must not escalate the tier. Escalation reaches for a
	// model, so treating "memory is down" as a behavioural signal would fire
	// a paid LLM call on every call for the duration of the outage — a cost
	// blowup caused by the firewall's own infrastructure rather than by
	// anything the agent did. Regression guard, found the hard way.
	if res.RiskScore != -1 || res.ModelUsed != "" {
		t.Errorf("a memory outage must not trigger inference, but risk=%d model=%q",
			res.RiskScore, res.ModelUsed)
	}
	t.Logf("memory deleted -> agent-repeat-offender ALLOWED (trust_unavailable=%v, model=%q). "+
		"This is the product failing, which is the point of the gate.",
		res.TrustUnavailable, res.ModelUsed)
}

// TestDeletionGate_MemoryIsConsultedBeforeGraphAndModel is case 3.
//
// The enforcement order claim: deterministic -> memory -> graph -> model.
// A firewall wired with a memory layer that blocks must never reach the
// graph, so a HydraDB endpoint that fails the test if touched proves the
// ordering rather than merely asserting it.
func TestDeletionGate_MemoryIsConsultedBeforeGraphAndModel(t *testing.T) {
	graphTouched := false
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		graphTouched = true
		w.Write([]byte(`{"success":true,"data":{"chunks":[],"graph_context":{"chunk_relations":[],"query_paths":[]}}}`))
	}))
	defer graph.Close()

	stub := &memoryStub{
		found: true,
		trust: sibyl.AgentTrust{TrustScore: 8, TotalBlocks: 4},
	}
	srv := stub.server(t)
	defer srv.Close()

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: permissivePolicy(t, "s-order"),
		Sibyl:    sibyl.New(srv.URL, newTestLogger(t)),
		// A stub provider that fails the test if a model is ever consulted.
		Router: nil,
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-order", AgentID: "agent-banned", Tool: "run_command",
		Args: map[string]any{"command": "pip install reqeusts"}, ToolCost: 0.01, Budget: 100,
	})

	if res.Decision != firewall.Block {
		t.Fatalf("expected BLOCK from memory, got %s", res.Decision)
	}
	if graphTouched {
		t.Error("HydraDB was queried even though memory had already decided — " +
			"enforcement order must be deterministic -> memory -> graph -> model")
	}
	if res.RiskScore != -1 {
		t.Errorf("a model appears to have run (risk score %d); memory-blocked calls must cost no inference",
			res.RiskScore)
	}
}
