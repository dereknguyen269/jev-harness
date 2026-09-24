package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/dereknguyen269/jev-harness/internal/approval"
	"github.com/dereknguyen269/jev-harness/internal/audit"
	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/harness"
)

// Gateway wires the V2 harness to HTTP. It serves /v2/* and keeps
// /v1/check + /health working for existing adapters.
type Gateway struct {
	Harness   *harness.Harness
	Approvals *approval.Store
	AuditPath string
	Timeout   time.Duration
	JevOn     bool
	Version   string
}

func (g *Gateway) timeout() time.Duration {
	if g.Timeout > 0 {
		return g.Timeout
	}
	return 10 * time.Second
}

func (g *Gateway) Router() *mux.Router {
	r := mux.NewRouter()
	r.HandleFunc("/health", g.handleHealth).Methods("GET")
	r.HandleFunc("/v2/health", g.handleHealth).Methods("GET")
	r.HandleFunc("/v1/check", g.handleV1Check).Methods("POST")
	r.HandleFunc("/v2/check", g.handleV2Check).Methods("POST")
	r.HandleFunc("/v2/approvals", g.handleListApprovals).Methods("GET")
	r.HandleFunc("/v2/approvals/{id}/approve", g.handleDecide(true)).Methods("POST")
	r.HandleFunc("/v2/approvals/{id}/deny", g.handleDecide(false)).Methods("POST")
	r.HandleFunc("/v2/audit", g.handleAudit).Methods("GET")
	r.HandleFunc("/v2/stats", g.handleStats).Methods("GET")
	r.HandleFunc("/v2/policies", g.handlePolicies).Methods("GET")
	return r
}

func (g *Gateway) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": g.Version, "jev": g.JevOn,
	})
}

// V2CheckRequest is the spec §18 request body.
type V2CheckRequest struct {
	Agent struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"agent"`
	Session map[string]string `json:"session"`
	Tool    struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	} `json:"tool"`
	Context domain.ExecutionContext `json:"context"`
}

func (g *Gateway) handleV2Check(w http.ResponseWriter, r *http.Request) {
	var req V2CheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Tool.Args == nil {
		req.Tool.Args = map[string]any{}
	}
	toolReq := domain.ToolRequest{
		ID:        uuid.NewString(),
		Agent:     domain.AgentInfo{Name: req.Agent.Name, Version: req.Agent.Version, Adapter: req.Agent.Name},
		Tool:      domain.ToolCall{Name: req.Tool.Name, Args: req.Tool.Args},
		Context:   req.Context,
		Timestamp: time.Now(),
	}
	if req.Session != nil {
		toolReq.Session = domain.SessionInfo{
			ID: req.Session["id"], TaskID: req.Session["task_id"], TurnID: req.Session["turn_id"],
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), g.timeout())
	defer cancel()
	writeJSON(w, http.StatusOK, g.Harness.Evaluate(ctx, toolReq))
}

// handleV1Check keeps legacy adapters working: translate v1 → ToolRequest.
func (g *Gateway) handleV1Check(w http.ResponseWriter, r *http.Request) {
	var v1 struct {
		SessionID string         `json:"session_id"`
		TaskID    string         `json:"task_id"`
		TurnID    string         `json:"turn_id"`
		Tool      string         `json:"tool"`
		Args      map[string]any `json:"args"`
		Context   struct {
			UserRequest string `json:"user_request"`
			WorkingDir  string `json:"working_dir"`
			Platform    string `json:"platform"`
			Agent       string `json:"agent"`
		} `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&v1); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if v1.Args == nil {
		v1.Args = map[string]any{}
	}
	agent := v1.Context.Agent
	if agent == "" {
		agent = v1.Context.Platform
	}
	if agent == "" {
		agent = "unknown"
	}
	toolReq := domain.ToolRequest{
		ID:      uuid.NewString(),
		Agent:   domain.AgentInfo{Name: agent, Adapter: agent},
		Session: domain.SessionInfo{ID: v1.SessionID, TaskID: v1.TaskID, TurnID: v1.TurnID},
		Tool:    domain.ToolCall{Name: v1.Tool, Args: v1.Args},
		Context: domain.ExecutionContext{
			Workspace: v1.Context.WorkingDir, WorkingDir: v1.Context.WorkingDir,
			Platform: v1.Context.Platform, UserRequest: v1.Context.UserRequest,
		},
		Timestamp: time.Now(),
	}
	ctx, cancel := context.WithTimeout(r.Context(), g.timeout())
	defer cancel()
	res := g.Harness.Evaluate(ctx, toolReq)
	// v1 shape for backward compat.
	writeJSON(w, http.StatusOK, map[string]any{
		"decision": res.Decision, "risk": res.Risk, "confidence": res.Confidence,
		"reason": res.Reason, "policy": map[string]string{"rule_id": res.PolicyID},
		"request_approval": res.RequestApproval,
	})
}

func (g *Gateway) handleListApprovals(w http.ResponseWriter, _ *http.Request) {
	if g.Approvals == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, g.Approvals.List())
}

func (g *Gateway) handleDecide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.Approvals == nil {
			http.Error(w, "approvals disabled", http.StatusServiceUnavailable)
			return
		}
		a, ok := g.Approvals.Decide(mux.Vars(r)["id"], approve)
		if !ok {
			http.Error(w, "approval not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

func (g *Gateway) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	events, err := audit.NewReader(g.AuditPath).Filter(
		q.Get("decision"), parseRisk(q.Get("min_risk")),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []domain.AuditEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (g *Gateway) handleStats(w http.ResponseWriter, _ *http.Request) {
	events, err := audit.NewReader(g.AuditPath).All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stats := map[string]int{"total": len(events)}
	for _, e := range events {
		stats[e.FinalDecision]++
	}
	writeJSON(w, http.StatusOK, stats)
}

func (g *Gateway) handlePolicies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": g.Version})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func parseRisk(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
