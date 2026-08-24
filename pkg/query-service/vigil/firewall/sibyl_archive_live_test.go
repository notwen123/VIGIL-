package firewall_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
)

// TestLiveArchiveTier drives the real firewall against a real running
// services/sibyl-memory and asserts a row actually lands in the ARCHIVE
// tier. It exists because the archive branch was unreachable for the whole
// life of the feature and no test noticed: sibyl_trust.go compared
// `trust.TrustScore < sibyl.TrustArchive` against a ladder that terminates
// at exactly TrustArchive, so the condition was false at every reachable
// score. A stub could not have caught that — the stub would have been
// written to agree with the same wrong arithmetic.
//
// The ladder, and why two violations is the whole of it:
//
//	strike 1  50 -> 30   recorded (blocked at the intent stage)
//	strike 2  30 -> 10   recorded (memory PAUSEs; a pause still counts)
//	                     10 <= TrustArchive -> archived
//	strike 3+            memory BLOCKs, and firewall.go excludes a
//	                     memory-stage block from recording, so trust never
//	                     moves again
//
// There is no third or fourth recorded strike to wait for. 10 is a floor
// the agent comes to rest on, not a value it passes through, which is
// exactly why the strict `<` never fired.
//
// Skipped unless a service is reachable, same gating as the other live
// tests, so the default offline `go test ./...` stays green:
//
//	SIBYL_DB_PATH=/tmp/archive-test.db python services/sibyl-memory/app.py &
//	VIGIL_SIBYL_URL=http://127.0.0.1:8765 go test -run TestLiveArchiveTier -v ./pkg/query-service/vigil/firewall/
func TestLiveArchiveTier(t *testing.T) {
	url := os.Getenv("VIGIL_SIBYL_URL")
	if url == "" {
		t.Skip("skipping: VIGIL_SIBYL_URL unset (start services/sibyl-memory/app.py)")
	}
	ctx := context.Background()
	client := sibyl.New(url, newTestLogger(t))
	if _, err := client.Health(ctx); err != nil {
		t.Skipf("skipping: memory service at %s is not answering: %v", url, err)
	}

	// A fresh identity every run, so the assertion is on a delta this test
	// caused rather than on whatever the database happened to hold.
	agentID := fmt.Sprintf("archive-probe-%d", time.Now().UnixNano())
	before := archivedCount(ctx, t, client)

	// The declared intent must ALLOW the tool. Intent is stage 1 and memory
	// is stage 2, so a tool denied by policy blocks before memory is ever
	// consulted and the ladder never starts. The first strike has to come
	// from a stage that runs *after* memory — here the blast-radius check
	// against the incident-response denylist, which is what the demo uses.
	t.Setenv("VIGIL_COMPROMISED_PACKAGES", "reqeusts")
	denylist := firewall.NewCompromisedList()

	sessionID := "archive-" + agentID
	store := policy.NewStore()
	p := &policy.Policy{
		SessionID:      sessionID,
		DeclaredIntent: "install dependencies",
		AllowedTools:   []string{"run_command"},
		NetworkAccess:  true,
		BudgetUSD:      5,
	}
	p.Normalize()
	store.Set(p)

	f := firewall.New(firewall.Deps{
		Logger:      newTestLogger(t),
		Policies:    store,
		Sibyl:       client,
		Compromised: denylist,
	})
	call := firewall.Call{
		SessionID: sessionID,
		AgentID:   agentID,
		Tool:      "run_command",
		Args:      map[string]any{"command": "pip install reqeusts"},
		ToolCost:  0.001,
		Budget:    5,
	}

	// Strike 1 — denied by declared intent, before memory has any record.
	res := f.Check(ctx, call)
	if res.Decision != firewall.Block {
		t.Fatalf("strike 1: want BLOCK, got %s (stage %s)", res.Decision, res.Stage)
	}
	t.Logf("strike 1: %s at stage %s", res.Decision, res.Stage)

	// Strike 2 — memory now recognises the agent and takes over. This is
	// the violation that lands trust on the archive floor.
	res = f.Check(ctx, call)
	if res.Stage != firewall.StageSibyl {
		t.Fatalf("strike 2: want the memory stage to adjudicate, got stage %s (%s)",
			res.Stage, res.Reason)
	}
	t.Logf("strike 2: %s at stage %s — %s", res.Decision, res.Stage, res.Reason)

	after := archivedCount(ctx, t, client)
	if after != before+1 {
		t.Fatalf("ARCHIVE tier did not grow: %d -> %d (want %d). "+
			"The archive branch in sibyl_trust.go is unreachable again — check "+
			"that the comparison against sibyl.TrustArchive is <=, not <.",
			before, after, before+1)
	}
	t.Logf("archived_entities %d -> %d", before, after)

	// Archiving must retire the agent, not absolve it. The row has left
	// `entities`, so a recall that only consulted the active set would
	// answer found=false — which the firewall reads as an agent it has
	// never seen and starts back at TrustDefault. That would make the
	// archive a get-out-of-jail card. The ban has to survive the move.
	trust, found, err := client.TrustScore(ctx, agentID)
	if err != nil {
		t.Fatalf("recall after archiving: %v", err)
	}
	if !found {
		t.Fatal("archiving un-banned the agent: recall returns found=false once " +
			"the row moves to archived_entities, so the next session sees a clean " +
			"agent at the default trust. /recall must fall back to the ARCHIVE tier.")
	}
	if trust.TrustScore != sibyl.TrustArchive {
		t.Fatalf("want the ladder to come to rest on the archive floor %d, got %d",
			sibyl.TrustArchive, trust.TrustScore)
	}
	if !trust.Banned("run_command") {
		t.Fatalf("archived agent lost its tool ban: banned_tools=%v", trust.BannedTools)
	}
	t.Logf("archived at trust %d after %d strikes, still banned from %v",
		trust.TrustScore, trust.TotalBlocks, trust.BannedTools)

	// And the decision that actually matters: a brand-new firewall, with no
	// denylist wired at all, still blocks — from the archive alone.
	fresh := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Sibyl:    client,
	})
	res = fresh.Check(ctx, call)
	if res.Decision != firewall.Block || res.Stage != firewall.StageSibyl {
		t.Fatalf("archived agent walked through a fresh firewall: %s at stage %s (%s)",
			res.Decision, res.Stage, res.Reason)
	}
	t.Logf("fresh firewall, no denylist: %s at stage %s — %s", res.Decision, res.Stage, res.Reason)
}

func archivedCount(ctx context.Context, t *testing.T, c *sibyl.Client) int {
	t.Helper()
	stats, err := c.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	n, ok := stats["archived_entities"].(float64)
	if !ok {
		t.Fatalf("stats has no archived_entities count (got %v) — "+
			"services/sibyl-memory/app.py must report the ARCHIVE tier", stats)
	}
	return int(n)
}
