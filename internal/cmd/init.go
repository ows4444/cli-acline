package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"acline/internal/scaffold"
	"acline/internal/store"
)

var (
	initYes         bool
	initName        string
	initAreas       string
	initRisk        string
	initHITL        bool
	initNoClaudeMD  bool
	initForceClaude bool
	initNoSnapshot  bool
	initFromJSON    string
	initNoScaffold  bool
	initUpgrade     bool
	initNoRegister  bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize ACLine and scaffold the Claude Code integration",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := dbPath
		if path == "" {
			p, err := store.DefaultPath()
			if err != nil {
				return err
			}
			path = p
		}
		_, statErr := os.Stat(path)
		isFresh := os.IsNotExist(statErr)

		s, err := store.Open(path)
		if err != nil {
			return err
		}
		defer s.Close()
		fmt.Println("initialized acline db at", path)

		if isFresh && !initNoSnapshot {
			if err := loadSnapshotSeed(s); err != nil {
				return err
			}
		}

		if !initNoScaffold {
			result, err := scaffold.WriteOpts(".", scaffold.Options{Upgrade: initUpgrade})
			if err != nil {
				return fmt.Errorf("scaffolding .claude: %w", err)
			}
			fmt.Printf("scaffolded .claude (%d created, %d merged, %d upgraded, %d existing left unchanged)\n",
				len(result.Created), len(result.Merged), len(result.Upgraded), len(result.Skipped))
			for _, u := range result.Upgraded {
				fmt.Println("upgraded:", u)
			}
			for _, w := range result.Warnings {
				fmt.Println("warning:", w)
			}
			if added, err := ensureGitignored(".", initGitignoreEntries); err != nil {
				fmt.Println("warning: could not update .gitignore:", err)
			} else if len(added) > 0 {
				fmt.Printf("added to .gitignore: %s\n", strings.Join(added, ", "))
			}
		}

		var answers initAnswers
		if !initNoClaudeMD {
			answers, err = writeClaudeMD(cmd.Flags().Changed("hitl"))
			if err != nil {
				return err
			}
		}
		if initNoRegister {
			return offerApprovalToken(s, !initYes && isInteractive(), askOnStdin)
		}
		name := answers.ProjectName
		if name == "" {
			name = initName
		}
		if name == "" {
			name = detectProjectName()
		}
		if err := registerCurrentProject(s, name, answers.DefaultHITL || initHITL); err != nil {
			return err
		}
		return offerApprovalToken(s, !initYes && isInteractive(), askOnStdin)
	},
}

// initGitignoreEntries are per-machine files acline writes inside a project:
// hook state, and the marker naming this checkout's project in this user's store.
var initGitignoreEntries = []string{".claude/vault/.state/", ".acline-project"}

// ensureGitignored appends the entries missing from dir/.gitignore, and returns
// them. It only touches a git checkout (a .git entry or an existing .gitignore).
func ensureGitignored(dir string, entries []string) ([]string, error) {
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if os.IsNotExist(err) {
		if _, gerr := os.Stat(filepath.Join(dir, ".git")); gerr != nil {
			return nil, nil // not a git checkout: nothing to ignore files from
		}
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, e := range entries {
		if !have[e] && !have["/"+e] && !have[strings.TrimSuffix(e, "/")] {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	var b strings.Builder
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n# acline: per-machine state\n")
	for _, m := range missing {
		b.WriteString(m + "\n")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return nil, err
	}
	return missing, f.Close()
}

// askOnStdin asks a yes/no question on the terminal init is running in.
func askOnStdin(prompt string) bool {
	return askYesNo(bufio.NewReader(os.Stdin), prompt, false)
}

// offerApprovalToken closes the gap between "approvals are enforced" (what the
// generated CLAUDE.md and SOUL.md say) and the default (identity is whatever
// ACLINE_ACTOR_TYPE says). Interactively it offers to enable the token now; in
// a script it only says how. It never fails init: declining is fine.
func offerApprovalToken(s *store.Store, interactive bool, confirm func(prompt string) bool) error {
	if on, err := s.ApprovalTokenEnabled(); err != nil || on {
		return err
	}
	if !interactive {
		fmt.Println("note: no approval token is enabled, so identity is self-declared and anything that sets ACLINE_ACTOR_TYPE=human can approve work.")
		fmt.Println("      Enforce approvals: run `acline auth init` in a terminal.")
		return nil
	}
	fmt.Println()
	if !confirm("Enable the approval token now? Agents then cannot approve work, override gates or loosen a task's risk") {
		fmt.Println("skipped — run `acline auth init` later; until then identity is self-declared.")
		return nil
	}
	if err := runAuthInit(s); err != nil {
		fmt.Printf("approval token not enabled: %v\n      run `acline auth init` from a terminal to try again.\n", err)
	}
	return nil
}

// loadSnapshotSeed treats acline.json (or --from-json) as the durable source of
// truth and the freshly-created SQLite file as a rebuildable cache in front
// of it. It's a no-op, quietly, when there's no snapshot to load — most
// first-time inits in a brand new project won't have one yet.
func loadSnapshotSeed(s *store.Store) error {
	path := initFromJSON
	if path == "" {
		path = defaultSnapshotPath
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	counts, err := s.LoadSnapshot(f)
	if err != nil {
		return fmt.Errorf("loading %s: %w", path, err)
	}
	printImportSummary(path, counts)
	return nil
}

// writeClaudeMD returns the answers it gathered (zero-valued when an existing
// CLAUDE.md was left alone) so init can register the project under the same
// name the user just confirmed.
func writeClaudeMD(hitlChanged bool) (initAnswers, error) {
	claudePath := "CLAUDE.md"
	if _, err := os.Stat(claudePath); err == nil && !initForceClaude {
		fmt.Printf("%s already exists — leaving it alone (use --force-claude-md to overwrite)\n", claudePath)
		return initAnswers{}, nil
	}

	answers := gatherInitAnswers(hitlChanged)

	content := renderClaudeMD(answers)
	if err := os.WriteFile(claudePath, []byte(content), 0o644); err != nil {
		return answers, fmt.Errorf("writing %s: %w", claudePath, err)
	}
	fmt.Printf("wrote %s\n", claudePath)
	return answers, nil
}

// registerCurrentProject tracks the directory init ran in and marks it as the
// current project. Without this, every project-scoped command runs unscoped,
// and `guard check-tool` blocks Edit/Write on the project's own files because
// its write scope is the vault plus registered project roots. A directory
// that's already registered just gets its marker refreshed; a name already
// taken by a different path is left alone with a hint rather than failing
// init.
func registerCurrentProject(s *store.Store, name string, hitl bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return err
	}
	projects, err := s.ListProjects()
	if err != nil {
		return err
	}
	for _, p := range projects {
		if p.Path.Valid && p.Path.String == cwd {
			if err := store.WriteMarker(cwd, p.Name); err != nil {
				return err
			}
			fmt.Printf("project already registered: %s (%s)\n", p.Name, cwd)
			return nil
		}
	}
	if existing, err := s.GetProjectByName(name); err == nil {
		fmt.Printf("warning: a project named %q is already registered at %s — not registering this directory; run `acline project add <name> %s` then `acline project use <name>`\n",
			name, existing.Path.String, cwd)
		return nil
	} else if !errors.Is(err, store.ErrProjectNotFound) {
		return err
	}
	autonomy := "hotl"
	if hitl {
		autonomy = "hitl"
	}
	var id int64
	err = withApprovalToken("registering project "+name, func(token string) error {
		var aerr error
		id, aerr = s.AddProjectWithToken(name, cwd, autonomy, token)
		return aerr
	})
	if err != nil {
		return err
	}
	if err := store.WriteMarker(cwd, name); err != nil {
		return err
	}
	fmt.Printf("project #%d registered and set as current: %s (%s)\n", id, name, cwd)
	return nil
}

// gatherInitAnswers asks the core onboarding questions interactively, or
// falls back to flags/defaults when running non-interactively (--yes, a
// piped stdin, or flags already given).
func gatherInitAnswers(hitlChanged bool) initAnswers {
	a := initAnswers{
		ProjectName: initName,
		DefaultRisk: initRisk,
		DefaultHITL: initHITL,
		Scaffold:    !initNoScaffold,
	}
	if initAreas != "" {
		a.Areas = splitTrim(initAreas)
	}

	interactive := !initYes && isInteractive()
	if !interactive {
		if a.ProjectName == "" {
			a.ProjectName = detectProjectName()
		}
		if a.DefaultRisk == "" {
			a.DefaultRisk = "low"
		}
		return a
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\nA few questions to shape CLAUDE.md (Enter to accept the default):")

	if a.ProjectName == "" {
		a.ProjectName = ask(reader, "Project name", detectProjectName())
	}
	if a.Areas == nil {
		areasRaw := ask(reader, "Primary areas, comma-separated (e.g. backend,frontend,mobile)", "")
		if areasRaw != "" {
			a.Areas = splitTrim(areasRaw)
		}
	}
	if a.DefaultRisk == "" {
		for {
			risk := ask(reader, "Default risk tier for new tasks (low/medium/high/critical)", "low")
			if store.ValidRisks[risk] {
				a.DefaultRisk = risk
				break
			}
			fmt.Printf("  %q is not one of low/medium/high/critical\n", risk)
		}
	}
	if !hitlChanged {
		a.DefaultHITL = askYesNo(reader, "Should new tasks default to requiring human approval (hitl) rather than review-after (hotl)?", false)
	}

	return a
}

func ask(r *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func askYesNo(r *bufio.Reader, prompt string, def bool) bool {
	suffix := "y/N"
	if def {
		suffix = "Y/n"
	}
	fmt.Printf("%s [%s]: ", prompt, suffix)
	line, _ := r.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	switch line {
	case "":
		return def
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

func splitTrim(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// defaultProjectName is the last-resort fallback when nothing in the repo
// (go.mod, package.json, git remote) suggests a name — see detectProjectName.
func defaultProjectName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "this project"
	}
	return filepath.Base(cwd)
}

// isInteractive reports whether stdin looks like a terminal rather than a
// pipe or redirected file, so init doesn't hang waiting for input in CI or
// when called from a script.
func isInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func init() {
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "skip interactive questions, use defaults/flags")
	initCmd.Flags().StringVar(&initName, "name", "", "project name (default: current directory name)")
	initCmd.Flags().StringVar(&initAreas, "areas", "", "comma-separated primary areas, e.g. backend,frontend,mobile")
	initCmd.Flags().StringVar(&initRisk, "risk", "", "default risk tier for new tasks (default: low)")
	initCmd.Flags().BoolVar(&initHITL, "hitl", false, "default new tasks to autonomy=hitl instead of hotl")
	initCmd.Flags().BoolVar(&initNoClaudeMD, "no-claude-md", false, "skip generating CLAUDE.md")
	initCmd.Flags().BoolVar(&initForceClaude, "force-claude-md", false, "overwrite an existing CLAUDE.md")
	initCmd.Flags().BoolVar(&initNoSnapshot, "no-snapshot", false, "don't load acline.json as a cache seed even if present")
	initCmd.Flags().StringVar(&initFromJSON, "from-json", "", "path to load as a cache seed (default: ./acline.json)")
	initCmd.Flags().BoolVar(&initNoScaffold, "no-claude-scaffold", false, "skip scaffolding .claude/hooks, .claude/skills, .claude/vault, and settings.json")
	initCmd.Flags().BoolVar(&initNoRegister, "no-register", false, "don't register the current directory as a tracked project")
	initCmd.Flags().BoolVar(&initUpgrade, "upgrade", false, "overwrite .claude/hooks and .claude/skills files that differ from the version bundled with this acline build (vault/ and settings.json are never overwritten this way -- review the diff before committing)")

	rootCmd.AddCommand(initCmd)
}
