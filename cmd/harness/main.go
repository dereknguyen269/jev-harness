package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"

	"github.com/dereknguyen269/jev-harness/internal/jev"
	"github.com/dereknguyen269/jev-harness/internal/policy"
)

type Server struct {
	engine  *policy.Engine
	audit   policy.AuditLogger
	cache   policy.Cache
	timeout time.Duration
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"version": "0.1.0",
		"jev":     s.engine != nil && s.engine.GetJevClient() != nil,
	})
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	var req policy.DecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		req.SessionID = "unknown"
	}
	if req.TaskID == "" {
		req.TaskID = "unknown"
	}
	if req.TurnID == "" {
		req.TurnID = "unknown"
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	resp, err := s.engine.Evaluate(ctx, req)
	if err != nil {
		resp = policy.DecisionResponse{
			Decision:        policy.Block,
			Risk:            0.9,
			Confidence:      0.8,
			Reason:          fmt.Sprintf("internal error: %v", err),
			RequestApproval: false,
		}
	}

	if s.audit != nil {
		s.audit.Log(policy.AuditEvent{
			ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
			Timestamp:  time.Now(),
			SessionID:  req.SessionID,
			TurnID:     req.TurnID,
			Tool:       req.Tool,
			Args:       req.Args,
			Decision:   resp.Decision,
			Risk:       resp.Risk,
			Confidence: resp.Confidence,
			RuleID:     resp.Policy.RuleID,
			Reason:     resp.Reason,
			DurationMS: time.Since(start).Milliseconds(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "audit endpoint not yet implemented - check audit log file",
	})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8787", "HTTP listen address")
	policyPath := flag.String("policy", "configs/policy.yaml", "Path to policy file")
	flag.Parse()

	pc, err := policy.Load(*policyPath)
	if err != nil {
		log.Fatalf("failed to load policy: %v", err)
	}

	je := jev.NewClient(os.Getenv("OPENROUTER_API_KEY"))
	cache := policy.NewCache(5 * time.Minute)
	eng := policy.NewEngine(pc, je, cache)

	auditPath := os.Getenv("AUDIT_PATH")
	audit, _ := policy.NewFileLogger(auditPath)

	srv := &Server{
		engine:  eng,
		audit:   audit,
		cache:   cache,
		timeout: 500 * time.Millisecond,
	}

	r := mux.NewRouter()
	r.HandleFunc("/health", srv.handleHealth).Methods("GET")
	r.HandleFunc("/v1/check", srv.handleCheck).Methods("POST")
	r.HandleFunc("/v1/audit", srv.handleAudit).Methods("GET")

	httpServer := &http.Server{Addr: *listen, Handler: r}
	go func() {
		log.Printf("harness listening on %s", *listen)
		log.Fatal(httpServer.ListenAndServe())
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(ctx)
}
