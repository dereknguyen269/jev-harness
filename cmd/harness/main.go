package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
			Reason:          "internal error",
			RequestApproval: false,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(resp)
		return
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

func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("failed to open %s: %v", path, err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[7:])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip inline comments: only when # is at start or preceded by whitespace
		if idx := strings.IndexAny(line, " \t#"); idx > 0 && line[idx] == '#' {
			line = strings.TrimSpace(line[:idx])
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		os.Setenv(key, value)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("error reading .env: %v", err)
	}
}

func main() {
	loadEnv(".env")

	listen := flag.String("listen", "127.0.0.1:8787", "HTTP listen address")
	policyPath := flag.String("policy", "configs/policy.yaml", "Path to policy file")
	jevEndpoint := flag.String("jev-endpoint", "", "Jev API endpoint URL (defaults to https://api.typesafe.ai/v1/systemone)")
	jevAPIKey := flag.String("jev-api-key", "", "Jev API key (overrides TYPESAFE_API_KEY / OPENROUTER_API_KEY env vars)")
	flag.Parse()

	pc, err := policy.Load(*policyPath)
	if err != nil {
		log.Fatalf("failed to load policy: %v", err)
	}

	jevAPIKeySet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "jev-api-key" {
			jevAPIKeySet = true
		}
	})
	apiKey := *jevAPIKey
	if !jevAPIKeySet {
		if apiKey == "" {
			apiKey = os.Getenv("JEV_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("TYPESAFE_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("OPENROUTER_API_KEY")
		}
	}
	endpoint := *jevEndpoint
	if endpoint == "" {
		endpoint = os.Getenv("JEV_ENDPOINT")
	}
	model := os.Getenv("JEV_MODEL")

	// Auto-select provider endpoint from env vars when not explicitly set
	if endpoint == "" {
		if os.Getenv("TYPESAFE_API_KEY") != "" {
			endpoint = "https://api.typesafe.ai/v1/systemone"
			if model == "" {
				model = "typesafe-ai/jev"
			}
		} else if os.Getenv("OPENROUTER_API_KEY") != "" {
			endpoint = "https://openrouter.ai/api/v1/decisions"
			if model == "" {
				model = "~typesafe/jev-latest"
			}
		} else if os.Getenv("JEV_API_KEY") != "" {
			endpoint = "https://api.typesafe.ai/v1/systemone"
			if model == "" {
				model = "typesafe-ai/jev"
			}
		}
	}
	// Override endpoint for evaluation models when endpoint not explicitly set via flag
	if *jevEndpoint == "" && (strings.HasPrefix(model, "typesafe-ai/") || strings.Contains(model, "jev")) {
		if strings.Contains(endpoint, "/v1/evaluate") {
			endpoint = "https://ai-gateway.vercel.sh/v1/evaluate"
		} else if strings.Contains(endpoint, "/v1/decisions") {
			endpoint = "https://openrouter.ai/api/v1/decisions"
		} else {
			endpoint = "https://api.typesafe.ai/v1/systemone"
		}
	}
	je := jev.NewClient(apiKey, endpoint, model, "")
	cache := policy.NewCache(5 * time.Minute)
	eng := policy.NewEngine(pc, je, cache)

	auditPath := os.Getenv("AUDIT_PATH")
	audit, _ := policy.NewFileLogger(auditPath)

	srv := &Server{
		engine:  eng,
		audit:   audit,
		cache:   cache,
		timeout: 10 * time.Second,
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
