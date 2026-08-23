package appserver

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
)

// registerMemoryRoutes mounts the cross-session memory, ACP and on-chain
// endpoints the dashboard reads.
func (st *stack) registerMemoryRoutes(api *mux.Router) {
	// --- memory health -------------------------------------------------------
	// The most important endpoint in this file. It answers "is cross-session
	// enforcement actually on", which is not the same question as "is memory
	// configured" — and confusing the two is the failure that lets an
	// operator believe repeat offenders are being stopped when they are not.
	api.HandleFunc("/vigil/memory/health", func(w http.ResponseWriter, r *http.Request) {
		if !st.sibyl.Configured() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"enforcing": false,
				"reason":    "memory layer disabled (VIGIL_SIBYL_DISABLED=1)",
				"impact":    "cross-session enforcement is OFF: repeat offenders will not be blocked",
			})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		h, err := st.sibyl.Health(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"enforcing": false,
				"reason":    "trust_score unavailable: " + err.Error(),
				"impact":    "cross-session enforcement is OFF: repeat offenders will not be blocked",
				"fix":       "start services/sibyl-memory (see MEMORY.md)",
			})
			return
		}
		h["enforcing"] = true
		writeJSON(w, http.StatusOK, h)
	}).Methods("GET", "OPTIONS")

	// --- tier counts for the HOT/WARM/COLD panel -----------------------------
	api.HandleFunc("/vigil/memory/stats", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		stats, err := st.sibyl.Stats(ctx)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "memory unavailable: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, stats)
	}).Methods("GET", "OPTIONS")

	// --- one agent's trust, for the blame-style timeline ---------------------
	api.HandleFunc("/vigil/memory/agent", func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("id")
		if agentID == "" {
			writeErr(w, http.StatusBadRequest, "query param 'id' is required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		started := time.Now()
		trust, found, err := st.sibyl.TrustScore(ctx, agentID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "trust_score unavailable: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"agent_id":  agentID,
			"found":     found,
			"trust":     trust,
			"recall_ms": float64(time.Since(started).Microseconds()) / 1000,
			"source":    "sibyl_memory(local sqlite, no llm)",
			"llm_calls": 0,
			"vectors":   0,
		})
	}).Methods("GET", "OPTIONS")

	// --- COLD journal, the timeline itself -----------------------------------
	api.HandleFunc("/vigil/memory/journal", func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
				limit = n
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		events, err := st.sibyl.ReadEvents(ctx, limit)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "memory unavailable: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(events), "events": events})
	}).Methods("GET", "OPTIONS")

	// --- ACP -----------------------------------------------------------------
	api.HandleFunc("/vigil/acp/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, st.acp.Status())
	}).Methods("GET", "OPTIONS")

	api.HandleFunc("/vigil/acp/jobs", func(w http.ResponseWriter, r *http.Request) {
		h := st.acp.History()
		writeJSON(w, http.StatusOK, map[string]any{"count": len(h), "jobs": h})
	}).Methods("GET", "OPTIONS")

	// The live job endpoint an ACP node drives.
	api.HandleFunc("/vigil/acp/job", st.acp.Handler()).Methods("POST", "OPTIONS")

	// --- on-chain ------------------------------------------------------------
	api.HandleFunc("/vigil/base/status", func(w http.ResponseWriter, r *http.Request) {
		receipts := st.anchorer.Recent()
		sent := 0
		for _, rc := range receipts {
			if rc.Sent {
				sent++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"anchoring_enabled": st.anchorer.Enabled(),
			"wallet":            st.anchorer.From(),
			"chain_id":          st.anchorer.ChainID(),
			"anchors_sent":      sent,
			"receipts":          receipts,
			"x402":              st.x402.Status(),
			// Stated rather than implied: an unanchored ledger is a
			// documented state, and no transaction hash appears anywhere
			// unless one was really sent.
			"note": "Anchoring activates on VIGIL_BASE_PRIVATE_KEY. Receipts with sent=false were never submitted.",
		})
	}).Methods("GET", "OPTIONS")
}
