package acp_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/SigNoz/signoz/pkg/acp"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/sibyl"
)

// The marketplace-reputation claim, made executable.
//
// VIGIL says it can refuse an ACP counterparty and explain why, from the
// same cross-session memory the firewall reads. Until this file existed
// that claim was the one in the repository backed only by code inspection —
// pkg/acp had no test files at all, so nothing verified that a low-trust
// buyer is actually refused or that a trusted one is actually accepted.
//
// This drives the real Service against a real services/sibyl-memory. Gated
// on VIGIL_SIBYL_URL like the other live tests, so the offline suite stays
// green and credential-free:
//
//	SIBYL_DB_PATH=/tmp/acp-test.db python services/sibyl-memory/app.py &
//	VIGIL_SIBYL_URL=http://127.0.0.1:8765 go test -v ./pkg/acp/
func TestLiveACPTrustGating(t *testing.T) {
	url := os.Getenv("VIGIL_SIBYL_URL")
	if url == "" {
		t.Skip("skipping: VIGIL_SIBYL_URL unset (start services/sibyl-memory/app.py)")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := sibyl.New(url, logger)
	if _, err := client.Health(ctx); err != nil {
		t.Skipf("skipping: memory service at %s is not answering: %v", url, err)
	}
	svc := acp.New(client, logger)

	// Fresh identities per run so the assertions are on records this test
	// wrote, not on whatever the database already held.
	stamp := time.Now().UnixNano()
	banned := fmt.Sprintf("acp-banned-%d", stamp)
	trusted := fmt.Sprintf("acp-trusted-%d", stamp)

	// Written through the same client the firewall writes trust with, so
	// the test exercises the real storage path rather than a fixture.
	seed := func(id string, score, blocks int) {
		t.Helper()
		if err := client.RememberAgent(ctx, id, sibyl.AgentTrust{
			TrustScore:        score,
			TotalBlocks:       blocks,
			LastViolationType: "typosquat",
		}); err != nil {
			t.Fatalf("seeding %s: %v", id, err)
		}
	}
	seed(banned, 12, 3)
	seed(trusted, 85, 0)

	cases := []struct {
		name string
		id   string
		want acp.Verdict
	}{
		// 12 < TrustACPBlock (30): a counterparty with a history of
		// violations is refused before any work is accepted.
		{"low trust is refused", banned, acp.VerdictBlock},
		// 85 >= TrustACPAllow (70): a clean counterparty is accepted.
		{"high trust is accepted", trusted, acp.VerdictAllow},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := svc.Evaluate(ctx, acp.Job{
				JobID:         "job-" + tc.id,
				BuyerAgentID:  tc.id,
				RequestedTool: "run_command",
				Intent:        "install a package",
			})
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if d.Verdict != tc.want {
				t.Fatalf("want %s, got %s — %s", tc.want, d.Verdict, d.Reason)
			}
			if !d.Recalled {
				t.Fatal("verdict was not backed by a memory recall")
			}
			// The claim is specifically that the refusal can say *why*.
			// A verdict with no recalled score behind it is an opinion.
			if tc.want == acp.VerdictBlock && d.PriorBlocks == 0 {
				t.Fatalf("a refusal must carry the prior-violation count, got %+v", d)
			}
			// No LLM on a marketplace request path — that is the whole
			// reason trust is a keyed read.
			if d.Source == "" {
				t.Fatal("decision has no source")
			}
			t.Logf("%s -> %s (trust %d, %d prior blocks, %.2fms, source %s)",
				tc.id, d.Verdict, d.TrustScore, d.PriorBlocks, d.RecallMS, d.Source)
		})
	}

	// An unknown counterparty is unproven, not trusted. Getting this
	// backwards would make the whole layer worthless: an attacker would
	// simply present a new agent id.
	t.Run("unknown counterparty is not auto-allowed", func(t *testing.T) {
		d, err := svc.Evaluate(ctx, acp.Job{
			JobID:         "job-unknown",
			BuyerAgentID:  fmt.Sprintf("acp-never-seen-%d", stamp),
			RequestedTool: "run_command",
		})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if d.Verdict == acp.VerdictAllow {
			t.Fatalf("an unseen counterparty must not be auto-allowed, got %s — %s",
				d.Verdict, d.Reason)
		}
		t.Logf("unknown -> %s — %s", d.Verdict, d.Reason)
	})
}
