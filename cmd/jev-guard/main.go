// Command jev-guard is the Agent Safety Gateway CLI.
//
//	jev-guard serve [--listen ADDR] [--policy PATH] [--profile NAME] [--reseed]
//	jev-guard check --tool NAME --command CMD [--env ENV]
//	jev-guard policy test [--policy PATH]
//	jev-guard policy reseed [--db PATH] [--mode merge|replace] [--force]
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
	"strconv"
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
	"github.com/dereknguyen269/jev-harness/internal/store"
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
		} else if len(os.Args) > 2 && os.Args[2] == "reseed" {
			cmdPolicyReseed(os.Args[3:])
		} else {
			fmt.Fprintln(os.Stderr, "usage: jev-guard policy <test|reseed> [--policy PATH]")
			os.Exit(1)
		}
	case "audit":
		cmdAudit(os.Args[2:])
	case "approvals":
		cmdApprovals(os.Args[2:])
	case "approve", "deny":
		cmdDecide(os.Args[1], os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "usage: jev-guard <serve|check|policy <test|reseed>|audit|approvals|approve <id>|deny <id>|eval|doctor|version>")
}

// loadPolicyForSeed loads the bundled policy for DB seeding: legacy rules
// plus V2 policies: converted to legacy shape (profiles use the V2 format).
// Legacy IDs win on collision. Groups merge de-duplicated.
func loadPolicyForSeed(policyPath, profile string) (*policy.PolicyConfig, error) {
	resolved := resolvePolicy(policyPath, profile)
	pc, err := policy.LoadMerged(resolved)
	if err != nil {
		return nil, err
	}
	v2cfg, _ := policy.LoadV2Merged(resolved)
	if v2cfg != nil {
		seen := map[string]bool{}
		for _, r := range pc.Rules {
			seen[r.ID] = true
		}
		for _, r := range policy.V2ToRules(v2cfg) {
			if !seen[r.ID] {
				seen[r.ID] = true
				pc.Rules = append(pc.Rules, r)
			}
		}
		seenGroup := map[string]bool{}
		for _, g := range pc.Groups {
			seenGroup[g.Name] = true
		}
		for _, g := range v2cfg.Groups {
			if !seenGroup[g.Name] {
				seenGroup[g.Name] = true
				pc.Groups = append(pc.Groups, g)
			}
		}
	}
	return pc, nil
}

// ---- shared wiring ----

type gateway struct {
	h   *harness.Harness
	eng *policy.Engine
}

func buildHarness(policyPath, profile string, jevAPIKey, jevEndpoint, jevModel string) (*harness.Harness, *policy.Engine, *approval.Store, bool) {
	return buildHarnessWithGroups(policyPath, profile, jevAPIKey, jevEndpoint, jevModel, nil)
}

func buildHarnessWithGroups(policyPath, profile string, jevAPIKey, jevEndpoint, jevModel string, groups []string) (*harness.Harness, *policy.Engine, *approval.Store, bool) {
	policyPath = resolvePolicy(policyPath, profile)
	log.Printf("using policy file: %s", policyPath)
	pc, err := policy.LoadMerged(policyPath)
	if err != nil {
		// Fall back to empty policy (fail-closed via judge).
		pc = &policy.PolicyConfig{}
		log.Printf("warning: policy load failed: %v", err)
	}
	v2cfg, _ := policy.LoadV2Merged(policyPath)
	legacy := policy.NewEngine(pc, nil, policy.NewCache(5*time.Minute))
	engV2 := policy.NewEngineV2(legacy, v2cfg)
	if len(groups) > 0 {
		engV2.SetActiveGroups(groups)
		log.Printf("active policy groups: %v", groups)
	}
	h := &harness.Harness{
		Policy:      engV2,
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
	policyPath := fs.String("policy", "", "Path to policy file or directory (overrides --profile and POLICY_PATH)")
	profile := fs.String("profile", os.Getenv("JEV_PROFILE"), "Policy profile (default|strict|developer|permissive)")
	groupFlag := fs.String("group", "", "Only evaluate these policy groups/categories/businesses/tasks (comma-separated, or POLICY_GROUPS)")
	jevEndpoint := fs.String("jev-endpoint", os.Getenv("JEV_ENDPOINT"), "Jev API endpoint")
	jevAPIKey := fs.String("jev-api-key", firstEnv("JEV_API_KEY", "TYPESAFE_API_KEY", "OPENROUTER_API_KEY"), "Jev API key")
	jevModel := fs.String("jev-model", os.Getenv("JEV_MODEL"), "Jev model")
	dbPath := fs.String("db", envOr("JEV_DB", defaultDBPath()), "SQLite path for dashboard users/rules (empty = YAML-only)")
	authToken := fs.String("auth-token", os.Getenv("JEV_AUTH_TOKEN"), "Dashboard auth token (empty = auth disabled)")
	approvalTTL := fs.Int("approval-ttl", envOrInt("JEV_APPROVAL_TTL", domain.DefaultApprovalTTLSeconds), "Default approval TTL in seconds (DB setting overrides at runtime)")
	reseed := fs.Bool("reseed", false, "Merge bundled YAML defaults into the DB on startup (adopts restructures, keeps custom rules)")
	_ = fs.Parse(args)

	groups := activeGroups(*groupFlag)
	h, legacy, approvals, jevOn := buildHarnessWithGroups(*policyPath, *profile, *jevAPIKey, *jevEndpoint, *jevModel, groups)
	h.SetApprovalTTLSeconds(*approvalTTL)
	log.Printf("default approval TTL: %ds", h.ApprovalTTLSeconds())
	engV2, _ := h.Policy.(*policy.EngineV2)

	// Optional SQLite store: DB rules become the runtime policy (seeded
	// from YAML on first run). Any failure → log and continue YAML-only;
	// the gateway must never refuse to start over a broken dashboard DB.
	var st *store.Store
	var seedPC *policy.PolicyConfig
	if *dbPath != "" {
		if s, err := store.Open(*dbPath); err != nil {
			log.Printf("warning: policy store unavailable (%v), running YAML-only", err)
		} else {
			st = s
			defer s.Close()
			pc, err := st.LoadPolicyConfig()
			if err != nil {
				log.Printf("warning: store policy load failed (%v), running YAML-only", err)
			} else {
				// YAML is loaded lazily and at most once: rules need it when
				// unseeded, groups whenever their table is empty (this also
				// backfills databases seeded before groups existed).
				var yamlPC *policy.PolicyConfig
				loadYAML := func() (*policy.PolicyConfig, bool) {
					if yamlPC != nil {
						return yamlPC, true
					}
					y, yerr := loadPolicyForSeed(*policyPath, *profile)
					if yerr != nil {
						log.Printf("warning: seed source unreadable (%v)", yerr)
						return nil, false
					}
					yamlPC = y
					seedPC = y
					return y, true
				}
				if pc == nil {
					if y, ok := loadYAML(); ok {
						if n, serr := st.Seed(y); serr != nil {
							log.Printf("warning: store seed failed (%v)", serr)
						} else {
							log.Printf("seeded policy store with %d rules from %s", n, resolvePolicy(*policyPath, *profile))
							pc, _ = st.LoadPolicyConfig()
						}
					}
				}
				if y, ok := loadYAML(); ok {
					if n, serr := st.SeedGroups(y); serr != nil {
						log.Printf("warning: group seed failed (%v)", serr)
					} else if n > 0 {
						log.Printf("seeded policy store with %d groups", n)
						pc, _ = st.LoadPolicyConfig()
					}
					if n, serr := st.SeedCategories(y); serr != nil {
						log.Printf("warning: category seed failed (%v)", serr)
					} else if n > 0 {
						log.Printf("seeded policy store with %d categories", n)
					}
					if *reseed {
						rn, gn, cn, rerr := st.SyncDefaults(y)
						if rerr != nil {
							log.Printf("warning: reseed failed (%v)", rerr)
						} else {
							pn, perr := st.PruneStaleDefaults(y)
							if perr != nil {
								log.Printf("warning: reseed prune failed (%v)", perr)
							} else if pn > 0 {
								log.Printf("reseed pruned %d stale defaults", pn)
							}
							log.Printf("reseeded policy store: %d rules, %d groups, %d categories", rn, gn, cn)
							pc, _ = st.LoadPolicyConfig()
						}
					}
				}
			}
			if pc != nil && engV2 != nil {
				engV2.Reload(pc, nil)
				log.Printf("policy loaded from store: %d rules", len(pc.Rules))
			}
		}
	}

	// Approvals persist to SQLite when the store is up; otherwise they
	// stay in memory (YAML-only mode). The store is the source of truth
	// across restarts, with the in-memory map as write-through fallback.
	if st != nil {
		approvals.SetPersistence(st)
		log.Print("approvals persisted to SQLite store")
		if j, ok := h.Judge.(*judge.Jev); ok && j != nil {
			j.SetCallPersistence(st)
			log.Print("jev calls persisted to SQLite store")
		}
		if secs, ok := st.GetApprovalTTLSeconds(); ok {
			h.SetApprovalTTLSeconds(secs)
			log.Printf("default approval TTL overridden from store: %ds", h.ApprovalTTLSeconds())
		} else if err := st.SetSetting(store.SettingApprovalTTLSeconds, strconv.Itoa(h.ApprovalTTLSeconds())); err != nil {
			log.Printf("warning: settings seed failed (%v)", err)
		}
	}

	gw := &server.Gateway{
		Harness: h, Approvals: approvals, Store: st,
		AuditPath: audit.ResolvePath(""), Timeout: 10 * time.Second,
		JevOn: jevOn, Version: version,
		Groups:     groupMetadata(legacy, engV2),
		AuthToken:  *authToken,
		SeedPolicy: seedPC,
	}
	if *authToken == "" {
		log.Print("warning: dashboard auth disabled (set --auth-token or JEV_AUTH_TOKEN to protect the dashboard)")
	}
	srv := &http.Server{Addr: *listen, Handler: gw.Router()}
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
	business := fs.String("business", "", "Business scope (e.g. payments)")
	task := fs.String("task", "", "Task scope (e.g. deploy)")
	category := fs.String("category", "", "Category scope (e.g. safety)")
	groupFlag := fs.String("group", "", "Only evaluate these policy groups (comma-separated, or POLICY_GROUPS)")
	policyPath := fs.String("policy", "", "Policy file or directory (overrides --profile)")
	profile := fs.String("profile", os.Getenv("JEV_PROFILE"), "Policy profile (default|strict|developer|permissive)")
	jevAPIKey := fs.String("jev-api-key", firstEnv("JEV_API_KEY", "TYPESAFE_API_KEY", "OPENROUTER_API_KEY"), "Jev API key")
	jevEndpoint := fs.String("jev-endpoint", os.Getenv("JEV_ENDPOINT"), "Jev API endpoint")
	jevModel := fs.String("jev-model", os.Getenv("JEV_MODEL"), "Jev model")
	_ = fs.Parse(args)
	if *command == "" && *path == "" {
		fmt.Fprintln(os.Stderr, "--command or --path required")
		os.Exit(1)
	}
	groups := activeGroups(*groupFlag)
	h, _, _, _ := buildHarnessWithGroups(*policyPath, *profile, *jevAPIKey, *jevEndpoint, *jevModel, groups)
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
		Context: domain.ExecutionContext{Environment: *env, Business: *business, Task: *task, Category: *category},
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
	policyPath := fs.String("policy", "configs/policy.yaml", "Policy file or directory")
	groupFlag := fs.String("group", "", "Only evaluate these policy groups (comma-separated, or POLICY_GROUPS)")
	_ = fs.Parse(args)
	h, _, _, _ := buildHarnessWithGroups(*policyPath, "", "", "", "", activeGroups(*groupFlag))
	h.Audit = nil
	type fixture struct {
		Name     string `yaml:"name"`
		Group    string `yaml:"group"`
		Tool     string `yaml:"tool"`
		Command  string `yaml:"command"`
		Path     string `yaml:"path"`
		Business string `yaml:"business"`
		Task     string `yaml:"task"`
		Category string `yaml:"category"`
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
				Context: domain.ExecutionContext{
					Business: c.Business, Task: c.Task, Category: c.Category,
				},
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

// ---- policy reseed ----

// cmdPolicyReseed merges restructured YAML defaults into an existing DB.
// Merge (default) upserts bundled rules/groups/categories and prunes stale
// yaml-sourced rows; dashboard customs (source='db') and users survive.
// Replace wipes all policy tables first and needs --force.
func cmdPolicyReseed(args []string) {
	fs := flag.NewFlagSet("policy reseed", flag.ExitOnError)
	dbPath := fs.String("db", envOr("JEV_DB", defaultDBPath()), "SQLite path (empty = YAML-only, nothing to do)")
	policyPath := fs.String("policy", "", "Policy file or directory (overrides --profile)")
	profile := fs.String("profile", os.Getenv("JEV_PROFILE"), "Policy profile (default|strict|developer|permissive)")
	mode := fs.String("mode", "merge", "Reseed mode: merge (keep customs) or replace (wipe policy tables)")
	force := fs.Bool("force", false, "Confirm --mode replace (destroys custom rules)")
	_ = fs.Parse(args)
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "reseed: no DB configured (YAML-only mode, nothing to do)")
		os.Exit(1)
	}
	pc, err := loadPolicyForSeed(*policyPath, *profile)
	if err != nil {
		log.Fatalf("reseed: load policy: %v", err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("reseed: open store: %v", err)
	}
	defer st.Close()
	switch *mode {
	case "replace":
		if !*force {
			fmt.Fprintln(os.Stderr, "reseed: --mode replace destroys custom rules; re-run with --force")
			os.Exit(1)
		}
		n, err := st.ReplaceWith(pc)
		if err != nil {
			log.Fatalf("reseed: replace: %v", err)
		}
		fmt.Printf("replaced policy store with %d rules from %s\n", n, resolvePolicy(*policyPath, *profile))
	case "merge", "":
		rn, gn, cn, err := st.SyncDefaults(pc)
		if err != nil {
			log.Fatalf("reseed: merge: %v", err)
		}
		pn, err := st.PruneStaleDefaults(pc)
		if err != nil {
			log.Fatalf("reseed: prune: %v", err)
		}
		fmt.Printf("reseeded %d rules, %d groups, %d categories, pruned %d stale defaults\n", rn, gn, cn, pn)
	default:
		fmt.Fprintf(os.Stderr, "reseed: unknown --mode %q (want merge|replace)\n", *mode)
		os.Exit(1)
	}
}

// splitDBFlag extracts --db PATH / --db=PATH from anywhere in args.
// The stdlib flag package stops parsing at the first positional argument,
// so `approve <id> --db X` would otherwise silently use the default DB.
// Returns the resolved path and the remaining args for the FlagSet.
func splitDBFlag(args []string) (string, []string) {
	db := envOr("JEV_DB", defaultDBPath())
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--db" && i+1 < len(args) {
			db = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(a, "--db=") {
			db = strings.TrimPrefix(a, "--db=")
			continue
		}
		rest = append(rest, a)
	}
	return db, rest
}

// ---- approvals (terminal approve/reject: same record as the dashboard) ----

// cmdApprovals lists approval history, newest first. Reads the SQLite store
// directly, so it works with or without a running server — the dashboard
// buttons and these commands converge on the same approvals table.
func cmdApprovals(args []string) {
	dbDefault, rest := splitDBFlag(args)
	fs := flag.NewFlagSet("approvals", flag.ExitOnError)
	dbPath := fs.String("db", dbDefault, "SQLite path (empty = YAML-only, no persisted history)")
	status := fs.String("status", "pending", "Filter: pending|all")
	limit := fs.Int("limit", 20, "Max rows (<=0 means all)")
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(rest)
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "approvals: no DB configured (YAML-only mode keeps approvals in server memory)")
		os.Exit(1)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("approvals: open store: %v", err)
	}
	defer st.Close()
	items, total, err := st.ListApprovalsPage(*limit, 0)
	if err != nil {
		log.Fatalf("approvals: list: %v", err)
	}
	if *status != "all" {
		kept := items[:0]
		for _, a := range items {
			if a.Status == domain.ApprovalPending {
				kept = append(kept, a)
			}
		}
		items = kept
	}
	if *asJSON {
		out, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(out))
		return
	}
	pending, err := st.PendingCount()
	if err != nil {
		log.Fatalf("approvals: count: %v", err)
	}
	fmt.Printf("%d approvals (%d pending), showing %d\n", total, pending, len(items))
	for _, a := range items {
		fmt.Printf("%s %-9s %-12s risk=%.2f expires=%s %s\n",
			a.ID, a.Status, a.Tool, a.Risk,
			a.ExpiresAt.Format(time.RFC3339), firstLine(a.Reason))
	}
}

// cmdDecide approves or denies one approval by ID prefix (e.g. the 8 chars
// shown by `approvals` and the dashboard). Same record the dashboard and
// polling agent plugins read, so a terminal decision unblocks them too.
func cmdDecide(cmd string, args []string) {
	approve := cmd == "approve"
	dbDefault, rest := splitDBFlag(args)
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dbPath := fs.String("db", dbDefault, "SQLite path (empty = YAML-only, nothing to decide)")
	_ = fs.Parse(rest)
	rest = fs.Args()
	if len(rest) == 0 {
		fmt.Fprintf(os.Stderr, "usage: jev-guard %s <approval-id-prefix> [--db PATH]\n", cmd)
		os.Exit(1)
	}
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, cmd+": no DB configured (YAML-only mode keeps approvals in server memory)")
		os.Exit(1)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("%s: open store: %v", cmd, err)
	}
	defer st.Close()
	prefix := rest[0]
	all, err := st.ListApprovals()
	if err != nil {
		log.Fatalf("%s: list: %v", cmd, err)
	}
	var match *domain.Approval
	ambiguous := false
	for i, a := range all {
		if strings.HasPrefix(string(a.ID), prefix) {
			if match != nil {
				ambiguous = true
				break
			}
			match = &all[i]
		}
	}
	if match == nil {
		fmt.Fprintf(os.Stderr, "%s: no approval starts with %q\n", cmd, prefix)
		os.Exit(1)
	}
	if ambiguous {
		fmt.Fprintf(os.Stderr, "%s: prefix %q is ambiguous, give more characters\n", cmd, prefix)
		os.Exit(1)
	}
	decided, err := st.DecideApproval(match.ID, approve)
	if err != nil {
		log.Fatalf("%s: decide: %v", cmd, err)
	}
	verb := "denied"
	if approve {
		verb = "approved"
	}
	fmt.Printf("%s %s (%s): %s on %s\n", verb, decided.ID, decided.Status, decided.Tool, firstLine(decided.Reason))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
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
		fmt.Printf("%s %-16s %-10s risk=%.2f conf=%.2f %s %s\n",
			e.Timestamp.Format(time.RFC3339), e.Tool, e.FinalDecision, e.Risk, e.Confidence, e.ReasonCode, auditTarget(e))
	}
}

// auditTarget prefers the most specific detail: command, then path, then resource.
func auditTarget(e domain.AuditEvent) string {
	switch {
	case e.Command != "":
		return e.Command
	case e.Path != "":
		return e.Path
	default:
		return e.Resource
	}
}

// ---- eval ----

func cmdEval(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: jev-guard eval <fixtures.yaml>")
		os.Exit(1)
	}
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	policyPath := fs.String("policy", "configs/policy.yaml", "Policy file or directory")
	groupFlag := fs.String("group", "", "Only evaluate these policy groups (comma-separated, or POLICY_GROUPS)")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		rest = args // no flags given
	}
	h, _, _, _ := buildHarnessWithGroups(*policyPath, "", "", "", "", activeGroups(*groupFlag))
	h.Audit = nil
	type fixture struct {
		Name     string `yaml:"name"`
		Group    string `yaml:"group"`
		Tool     string `yaml:"tool"`
		Command  string `yaml:"command"`
		Path     string `yaml:"path"`
		Business string `yaml:"business"`
		Task     string `yaml:"task"`
		Category string `yaml:"category"`
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
				Context: domain.ExecutionContext{
					Business: c.Business, Task: c.Task, Category: c.Category,
				},
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
	if pc, derr := policy.Load("configs/policy.yaml"); derr != nil {
		ok("Policy defaults", false, derr.Error())
	} else {
		groups := map[string]bool{}
		for _, g := range pc.Groups {
			groups[g.Name] = true
		}
		untagged, ungrouped := 0, 0
		for _, r := range pc.Rules {
			if r.Category == "" {
				untagged++
			}
			if r.Group != "" && !groups[r.Group] {
				ungrouped++
			}
		}
		detail := fmt.Sprintf("%d rules %d groups (%d untagged, %d undeclared-group)",
			len(pc.Rules), len(pc.Groups), untagged, ungrouped)
		ok("Policy defaults", untagged == 0 && ungrouped == 0, detail)
	}
	_, err = os.Stat("configs/profiles/default.yaml")
	ok("Profiles", err == nil, firstErr(err, "profiles present"))
	ok("HTTP server", true, "routes /v1/check /v1/approvals /v1/audit (run jev-guard serve)")
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

// activeGroups merges --group flag with POLICY_GROUPS env.
func activeGroups(flag string) []string {
	if flag != "" {
		return policy.ParseActiveGroups(flag)
	}
	return policy.ParseActiveGroups(os.Getenv("POLICY_GROUPS"))
}

// groupMetadata merges legacy + policy-engine group declarations for /v1/policies.
func groupMetadata(legacy *policy.Engine, engV2 *policy.EngineV2) []map[string]any {
	seen := map[string]bool{}
	var out []map[string]any
	if engV2 != nil {
		for _, g := range engV2.Groups() {
			if !seen[g.Name] {
				seen[g.Name] = true
				out = append(out, map[string]any{"name": g.Name, "description": g.Description})
			}
		}
	} else if legacy != nil {
		for _, g := range legacy.Groups() {
			if !seen[g.Name] {
				seen[g.Name] = true
				out = append(out, map[string]any{"name": g.Name, "description": g.Description})
			}
		}
	}
	return out
}

// defaultDBPath mirrors the audit convention: ~/.hermes/guard/jev.db.
func defaultDBPath() string {
	if h := os.Getenv("HOME"); h != "" {
		return h + "/.hermes/guard/jev.db"
	}
	return "jev.db"
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envOrInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
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
