// Command jev-guard is the V2 Agent Safety Gateway CLI.
//
//	jev-guard serve [--listen ADDR] [--policy PATH] [--profile NAME]
//	jev-guard check --tool NAME --command CMD [--env ENV]
//	jev-guard policy test [--policy PATH]
//	jev-guard audit [--decision D] [--min-risk F] [--json]
//	jev-guard eval <fixtures.yaml>
//	jev-guard doctor
//	jev-guard version
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

	"github.com/dereknguyen269/jev-harness/internal/approval"
	"github.com/dereknguyen269/jev-harness/internal/audit"
	"github.com/dereknguyen269/jev-harness/internal/cache"
	"github.com/dereknguyen269/jev-harness/internal/config"
	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/harness"
	"github.com/dereknguyen269/jev-harness/internal/judge"
	"github.com/dereknguyen269/jev-harness/internal/policy"
	"github.com/dereknguyen269/jev-harness/internal/server"
	"gopkg.in/yaml.v3"
)

const version = "0.2.0"

func main() {
	loadEnv(".env")
	loadEnv(os.Getenv("HOME") + "/.config/jev-guard/.env")
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "check":
		cmdCheck(os.Args[2:])
	case "policy":
		if len(os.Args) > 2 && os.Args[2] == "test" {
			cmdPolicyTest(os.Args[3:])
		} else {
			fmt.Fprintln(os.Stderr, "usage: jev-guard policy test [--policy PATH]")
			os.Exit(1)
		}
	case "audit":
		cmdAudit(os.Args[2:])
	case "eval":
		cmdEval(os.Args[2:])
	case "doctor":
		cmdDoctor(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("jev-guard", version)
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: jev-guard <serve|check|policy test|audit|eval|doctor|version>")
}

// ---- shared wiring ----

type gateway struct {
	h   *harness.Harness
	eng *policy.Engine
}

func buildHarness(policyPath, profile string, jevAPIKey, jevEndpoint, jevModel string) (*harness.Harness, *policy.Engine, *approval.Store, bool) {
	policyPath = resolvePolicy(policyPath, profile)
	log.Printf("using policy file: %s", policyPath)
	pc, err := policy.Load(policyPath)
	if err != nil {
		// Fall back to empty policy (fail-closed via judge).
		pc = &policy.PolicyConfig{}
		log.Printf("warning: policy load failed: %v", err)
	}
	v2cfg, _ := policy.LoadV2(policyPath)
	legacy := policy.NewEngine(pc, nil, policy.NewCache(5*time.Minute))
	h := &harness.Harness{
		Policy:      policy.NewEngineV2(legacy, v2cfg),
		Cache:       cache.New(),
		Thresholds:  domain.DefaultThresholds(),
		ApprovalTTL: 30 * time.Second,
	}
	approvals := approval.NewStore()
	h.Approvals = approvals
	jevOn := false
	if jevAPIKey != "" {
		h.Judge = judge.NewJevFromClient(jevAPIKey, jevEndpoint, jevModel)
		jevOn = true
	}
	auditW, _ := audit.NewWriter("")
	if auditW != nil {
		h.Audit = auditW
	}
	return h, legacy, approvals, jevOn
}

// resolvePolicy picks the policy file: explicit --policy (or POLICY_PATH)
// wins, then --profile bundle, then the default configs/policy.yaml.
func resolvePolicy(policyFlag, profile string) string {
	if policyFlag != "" {
		return policyFlag
	}
	if v := os.Getenv("POLICY_PATH"); v != "" {
		return v
	}
	if profile != "" {
		if p := config.ProfileFile(config.Profile(profile)); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
			log.Printf("warning: profile %q not found at %s", profile, p)
		}
	}
	return "configs/policy.yaml"
}

func cmdServe(args []string) {
	loadEnv(".env")
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", envOr("LISTEN", "127.0.0.1:8787"), "HTTP listen address")
	policyPath := fs.String("policy", "", "Path to policy file (overrides --profile and POLICY_PATH)")
	profile := fs.String("profile", os.Getenv("JEV_PROFILE"), "Policy profile (default|strict|developer|permissive)")
	jevEndpoint := fs.String("jev-endpoint", os.Getenv("JEV_ENDPOINT"), "Jev API endpoint")
	jevAPIKey := fs.String("jev-api-key", firstEnv("JEV_API_KEY", "TYPESAFE_API_KEY", "OPENROUTER_API_KEY"), "Jev API key")
	jevModel := fs.String("jev-model", os.Getenv("JEV_MODEL"), "Jev model")
	_ = fs.Parse(args)

	h, _, approvals, jevOn := buildHarness(*policyPath, *profile, *jevAPIKey, *jevEndpoint, *jevModel)
	gw := &server.Gateway{
		Harness: h, Approvals: approvals,
		AuditPath: audit.ResolvePath(""), Timeout: 10 * time.Second,
		JevOn: jevOn, Version: version,
	}
	r := gw.Router()
	// Legacy compat routes.
	r.HandleFunc("/v1/audit", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "use GET /v2/audit"})
	}).Methods("GET")

	srv := &http.Server{Addr: *listen, Handler: r}
	go func() {
		log.Printf("jev-guard %s listening on %s (profile=%s policy=%s)", version, *listen, *profile, *policyPath)
		log.Fatal(srv.ListenAndServe())
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

// ---- check ----

func cmdCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	tool := fs.String("tool", "terminal", "Tool name")
	command := fs.String("command", "", "Command to check")
	path := fs.String("path", "", "File path (for write tools)")
	env := fs.String("env", "", "Environment (e.g. production)")
	policyPath := fs.String("policy", "", "Policy file (overrides --profile)")
	profile := fs.String("profile", os.Getenv("JEV_PROFILE"), "Policy profile (default|strict|developer|permissive)")
	jevAPIKey := fs.String("jev-api-key", firstEnv("JEV_API_KEY", "TYPESAFE_API_KEY", "OPENROUTER_API_KEY"), "Jev API key")
	jevEndpoint := fs.String("jev-endpoint", os.Getenv("JEV_ENDPOINT"), "Jev API endpoint")
	jevModel := fs.String("jev-model", os.Getenv("JEV_MODEL"), "Jev model")
	_ = fs.Parse(args)
	if *command == "" && *path == "" {
		fmt.Fprintln(os.Stderr, "--command or --path required")
		os.Exit(1)
	}
	h, _, _, _ := buildHarness(*policyPath, *profile, *jevAPIKey, *jevEndpoint, *jevModel)
	h.Audit = nil // no audit for one-shot CLI checks
	argMap := map[string]any{}
	if *command != "" {
		argMap["command"] = *command
	}
	if *path != "" {
		argMap["path"] = *path
	}
	res := h.Evaluate(context.Background(), domain.ToolRequest{
		ID:      "cli",
		Agent:   domain.AgentInfo{Name: "cli"},
		Tool:    domain.ToolCall{Name: *tool, Args: argMap},
		Context: domain.ExecutionContext{Environment: *env},
	})
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
	if res.Decision == domain.Block {
		os.Exit(2)
	}
	if res.Decision == domain.ApprovalRequired {
		os.Exit(3)
	}
}

// ---- policy test ----

func cmdPolicyTest(args []string) {
	fs := flag.NewFlagSet("policy test", flag.ExitOnError)
	policyPath := fs.String("policy", "configs/policy.yaml", "Policy file")
	_ = fs.Parse(args)
	h, _, _, _ := buildHarness(*policyPath, "", "", "", "")
	h.Audit = nil
	type fixture struct {
		Name     string `yaml:"name"`
		Tool     string `yaml:"tool"`
		Command  string `yaml:"command"`
		Path     string `yaml:"path"`
		Expected string `yaml:"expected"`
	}
	for _, f := range []string{"evals/fixtures/policy.yaml"} {
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Printf("no fixtures at %s\n", f)
			continue
		}
		var cases []fixture
		if err := yamlUnmarshal(data, &cases); err != nil {
			log.Fatalf("parse %s: %v", f, err)
		}
		pass, fail := 0, 0
		for _, c := range cases {
			argMap := map[string]any{}
			if c.Command != "" {
				argMap["command"] = c.Command
			}
			if c.Path != "" {
				argMap["path"] = c.Path
			}
			res := h.Evaluate(context.Background(), domain.ToolRequest{
				Agent: domain.AgentInfo{Name: "eval"},
				Tool:  domain.ToolCall{Name: c.Tool, Args: argMap},
			})
			if string(res.Decision) == c.Expected {
				pass++
			} else {
				fail++
				fmt.Printf("FAIL %s: expected %s got %s (%s)\n", c.Name, c.Expected, res.Decision, res.Reason)
			}
		}
		fmt.Printf("%s: %d pass %d fail\n", f, pass, fail)
		if fail > 0 {
			os.Exit(1)
		}
	}
}

// ---- audit ----

func cmdAudit(args []string) {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	decision := fs.String("decision", "", "Filter by final decision")
	minRisk := fs.Float64("min-risk", 0, "Minimum risk")
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)
	events, err := audit.NewReader("").Filter(*decision, *minRisk)
	if err != nil {
		log.Fatal(err)
	}
	if *asJSON {
		out, _ := json.MarshalIndent(events, "", "  ")
		fmt.Println(string(out))
		return
	}
	for _, e := range events {
		fmt.Printf("%s %-16s %-10s risk=%.2f conf=%.2f %s\n",
			e.Timestamp.Format(time.RFC3339), e.Tool, e.FinalDecision, e.Risk, e.Confidence, e.ReasonCode)
	}
}

// ---- eval ----

func cmdEval(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: jev-guard eval <fixtures.yaml>")
		os.Exit(1)
	}
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	policyPath := fs.String("policy", "configs/policy.yaml", "Policy file")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		rest = args // no flags given
	}
	h, _, _, _ := buildHarness(*policyPath, "", "", "", "")
	h.Audit = nil
	type fixture struct {
		Name     string `yaml:"name"`
		Tool     string `yaml:"tool"`
		Command  string `yaml:"command"`
		Path     string `yaml:"path"`
		Expected string `yaml:"expected"`
	}
	total, allow, appr, block, falseAllow, falseBlock := 0, 0, 0, 0, 0, 0
	start := time.Now()
	var worst time.Duration
	for _, f := range rest {
		data, err := os.ReadFile(f)
		if err != nil {
			log.Fatalf("read %s: %v", f, err)
		}
		var cases []fixture
		if err := yamlUnmarshal(data, &cases); err != nil {
			log.Fatalf("parse %s: %v", f, err)
		}
		for _, c := range cases {
			argMap := map[string]any{}
			if c.Command != "" {
				argMap["command"] = c.Command
			}
			if c.Path != "" {
				argMap["path"] = c.Path
			}
			t0 := time.Now()
			res := h.Evaluate(context.Background(), domain.ToolRequest{
				Agent: domain.AgentInfo{Name: "eval"},
				Tool:  domain.ToolCall{Name: c.Tool, Args: argMap},
			})
			if d := time.Since(t0); d > worst {
				worst = d
			}
			total++
			switch res.Decision {
			case domain.Allow:
				allow++
			case domain.ApprovalRequired:
				appr++
			case domain.Block:
				block++
			}
			if string(res.Decision) != c.Expected {
				fmt.Printf("MISS %-24s expected=%-16s got=%-16s\n", c.Name, c.Expected, res.Decision)
				if c.Expected == "block" || c.Expected == "approval_required" {
					falseAllow++
				} else {
					falseBlock++
				}
			}
		}
	}
	acc := 0.0
	if total > 0 {
		acc = float64(total-falseAllow-falseBlock) / float64(total) * 100
	}
	fmt.Printf("\nPolicy Evaluation\nCases: %d  Allow: %d  Approval: %d  Block: %d\nFalse Allow: %d  False Block: %d\nAccuracy: %.1f%%  Worst latency: %s  Total: %s\n",
		total, allow, appr, block, falseAllow, falseBlock, acc, worst.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
	if falseAllow > 0 {
		os.Exit(1)
	}
}

// ---- doctor ----

func cmdDoctor(_ []string) {
	fmt.Println("Jev Guard Doctor")
	ok := func(name string, good bool, detail string) {
		mark := "✓"
		if !good {
			mark = "✗"
		}
		fmt.Printf("%s %-18s %s\n", mark, name, detail)
	}
	ok("Go runtime", true, "ok")
	_, err := policy.Load("configs/policy.yaml")
	ok("Config", err == nil, firstErr(err, "configs/policy.yaml loaded"))
	_, err = os.Stat("configs/profiles/default.yaml")
	ok("Profiles", err == nil, firstErr(err, "profiles present"))
	ok("HTTP server", true, "routes /v2/check /v2/approvals /v2/audit (run jev-guard serve)")
	hasKey := firstEnv("JEV_API_KEY", "TYPESAFE_API_KEY", "OPENROUTER_API_KEY") != ""
	status := "not set (fail-closed, policy-only)"
	if hasKey {
		status = "set"
	}
	ok("Jev API", true, status)
	for _, a := range []struct{ name, path string }{
		{"OpenCode adapter", "plugins/opencode/jev-guard.js"},
		{"Kiro adapter", "plugins/kiro/jev-guard.py"},
		{"Claude adapter", "adapters/claude/jev-guard.py"},
		{"Codex adapter", "adapters/codex/jev-guard.py"},
		{"OpenClaw adapter", "adapters/openclaw/jev-guard.py"},
	} {
		_, err := os.Stat(a.path)
		ok(a.name, err == nil, a.path)
	}
	ok("Audit", true, audit.ResolvePath(""))
	fmt.Println("\nOverall: READY")
}

// ---- helpers ----

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func firstErr(err error, ok string) string {
	if err != nil {
		return err.Error()
	}
	return ok
}

func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func yamlUnmarshal(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}
