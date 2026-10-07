package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"acline/internal/orchestrate"
	"acline/internal/store"
)

// orchFlags are the persistent flags every orchestrate subcommand reads, and
// the store they act on.
type orchFlags struct {
	c           *cli
	agentBin    string
	timeout     time.Duration
	stepBudget  float64
	allowTools  []string
	passEnv     []string
	noSandbox   bool
	unprotected bool
	dryRun      bool
	project     string
	showPrompt  bool
}

func newOrchestrateCmd(c *cli) *cobra.Command {
	f := &orchFlags{c: c}
	cmd := &cobra.Command{
		Use:   "orchestrate",
		Short: "Run a task's next step with an agent, bounded and supervised",
		Long: "Routes a task (see `acline next`), assembles its brief, and runs the step in one non-interactive Claude Code session,\n" +
			"then re-reads the recorded state. It advances work; it never vouches for it: it runs as an agent identity, never sees the\n" +
			"approval token, only launches steps an agent may take on tasks that are not hitl and not high/critical risk, and never\n" +
			"completes a task. It stops when a person is needed, nothing changed, or a budget is reached.\n\n" +
			"Launching requires an approval token to be enabled (`acline auth init`), because without one an agent could act as a\n" +
			"human and approve its own work. Run it from your terminal, not from an agent session.",
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&f.agentBin, "agent-bin", "claude", "the Claude Code binary to run")
	pf.DurationVar(&f.timeout, "timeout", 15*time.Minute, "kill a step that runs longer than this (the installed claude has no turn limit, so this is the bound)")
	pf.Float64Var(&f.stepBudget, "step-budget", 1.00, "spend cap in USD handed to each agent run")
	pf.StringSliceVar(&f.allowTools, "allow-tool", nil, "extra tool permission for the agent, e.g. 'Bash(make *)' (repeatable)")
	pf.BoolVar(&f.noSandbox, "no-sandbox", false, "launch the agent without Claude Code's Bash sandbox. By default every step runs its shell commands sandboxed: no reads of ~/.ssh, ~/.aws and other credential files, writes only to the project, the store and the Go caches, network only to the Go module proxy; a platform without the sandbox refuses to launch")
	pf.StringSliceVar(&f.passEnv, "pass-env", nil, "extra environment variable NAME the agent may inherit, e.g. AWS_PROFILE (repeatable). The agent otherwise gets only PATH, HOME, locale, proxy, Go, ANTHROPIC_* and CLAUDE_* variables")
	pf.BoolVar(&f.unprotected, "allow-unprotected", false, "launch even though no approval token is enabled or the project's guard hook is missing or incomplete (an agent could then act as a human, or run unscreened)")
	pf.BoolVar(&f.dryRun, "dry-run", false, "show what would run and launch nothing")
	pf.StringVar(&f.project, "project", "", "project name (only when no task id is given)")
	// A persistent flag, not per-subcommand: plan, spec and research read showPrompt.
	pf.BoolVar(&f.showPrompt, "show-prompt", false, "with --dry-run, also print the prompt that would be sent (plan/spec only)")
	cmd.AddCommand(newOrchestrateStepCmd(f), newOrchestrateRunCmd(f), newOrchestratePlanCmd(f), newOrchestrateSpecCmd(f), newOrchestrateResearchCmd(f), newOrchestrateStopCmd(f))
	return cmd
}

func (f *orchFlags) stopFile() string {
	return filepath.Join(filepath.Dir(f.c.st.Path), "orchestrator.stop")
}

// preflight refuses to launch unless an agent cannot trivially act as
// a person (approval token enabled) and the project actually runs the guard on
// the tools the agent will use. --allow-unprotected accepts both risks. It
// checks the guard in the working directory, where plan, spec and research
// run; step and run check the task's project instead (Options.GuardTools).
func (f *orchFlags) preflight() error {
	if err := orchestrate.Preflight(f.c.st, f.unprotected); err != nil {
		return err
	}
	if f.unprotected {
		return nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return orchestrate.PreflightHooks(dir, guardToolNames())
}

func guardToolNames() []string {
	tools := make([]string, 0, len(requiredGuardTools()))
	for name := range requiredGuardTools() {
		tools = append(tools, name)
	}
	return tools
}

func (f *orchFlags) options(args []string, maxSteps int) (orchestrate.Options, error) {
	o := orchestrate.Options{
		MaxSteps: maxSteps, StepBudgetUSD: f.stepBudget, StepTimeout: f.timeout,
		ExtraAllow: f.allowTools, PassEnv: f.passEnv, StopFile: f.stopFile(), DryRun: f.dryRun, NoSandbox: f.noSandbox,
		Log: func(format string, a ...any) { fmt.Printf(format+"\n", a...) },
	}
	if len(args) == 1 {
		id, err := parseID(args[0], "task")
		if err != nil {
			return o, err
		}
		o.TaskID = &id
	} else {
		// The current project, like any command that acts: without it the next
		// task could come from any project in the shared store.
		pid, err := f.c.resolveProjectFlag(f.project)
		if err != nil {
			return o, err
		}
		o.ProjectID = pid
	}
	var err error
	o.Dir, err = os.Getwd()
	return o, err
}

// asAgent makes this process act as the orchestrator: an agent identity, never
// the human at the keyboard, so nothing it records can pass for a person's.
func (f *orchFlags) asAgent() {
	f.c.st.Actor = store.Actor{Type: "agent", ID: "orchestrator"}
}

func (f *orchFlags) prepare(args []string, maxSteps int) (orchestrate.Options, error) {
	o, err := f.options(args, maxSteps)
	if err != nil {
		return o, err
	}
	if !f.dryRun {
		if err := orchestrate.Preflight(f.c.st, f.unprotected); err != nil {
			return o, err
		}
		if !f.unprotected {
			o.GuardTools = guardToolNames()
		}
	}
	f.asAgent()
	return o, nil
}

func printStep(r orchestrate.StepReport) {
	if r.TaskID == 0 {
		fmt.Printf("nothing to do: %s\n", r.Detail)
		return
	}
	fmt.Printf("task #%d  %s", r.TaskID, r.Action)
	if r.Role != "" {
		fmt.Printf("  (role %s)", r.Role)
	}
	fmt.Println()
	if r.Launched {
		fmt.Printf("  ran %s, exit %d", r.Duration.Round(time.Second), r.ExitCode)
		if r.TimedOut {
			fmt.Print(", timed out")
		}
		fmt.Printf(", cost $%.2f\n  state: %s -> %s\n", r.CostUSD, r.Action, r.After)
	}
	if r.Launched {
		if r.FilesEdited {
			fmt.Println("  files were changed in the working directory")
		}
		if r.Summary != "" {
			fmt.Printf("  agent said: %s\n", strings.ReplaceAll(r.Summary, "\n", "\n              "))
		}
		for _, d := range r.Denied {
			fmt.Printf("  permission denied: %s\n", d)
		}
	}
	if len(r.Command) > 0 {
		fmt.Printf("  would run: %s\n", strings.Join(r.Command, " "))
	}
	if r.Stop != orchestrate.StopNone {
		fmt.Printf("  stopped: %s — %s\n", r.Stop, r.Detail)
	}
}

func newOrchestrateStepCmd(f *orchFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "step [task-id]",
		Short: "Run one bounded agent step for a task (or the best launchable one)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := f.prepare(args, 1)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			r, err := orchestrate.Step(ctx, f.c.st, orchestrate.ClaudeAgent{Bin: f.agentBin}, o)
			if err != nil {
				return err
			}
			printStep(r)
			return nil
		},
	}
	return cmd
}

func newOrchestrateRunCmd(f *orchFlags) *cobra.Command {
	var (
		orchMaxCost  float64
		orchMaxSteps int
	)
	cmd := &cobra.Command{
		Use:   "run [task-id]",
		Short: "Repeat steps until a person is needed, nothing changes, or a limit is reached",
		Long: "Only tasks with autonomy=auto chain: a hotl task gets one step and then a review point. Stops on the first of:\n" +
			"a person is needed, the gate is satisfied (completing is yours), no progress, an agent failure or policy denial,\n" +
			"--max-steps, --max-cost, or the stop file (`acline orchestrate stop`).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := f.prepare(args, orchMaxSteps)
			if err != nil {
				return err
			}
			o.MaxCostUSD = orchMaxCost
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			rep, err := orchestrate.Run(ctx, f.c.st, orchestrate.ClaudeAgent{Bin: f.agentBin}, o)
			if err != nil {
				return err
			}
			for _, s := range rep.Steps {
				printStep(s)
			}
			fmt.Printf("\nstopped: %s — %s\n%d step(s), $%.2f spent\n", rep.Stop, rep.Detail, len(rep.Steps), rep.CostUSD)
			return nil
		},
	}
	cmd.Flags().IntVar(&orchMaxSteps, "max-steps", 5, "stop after this many launches")
	cmd.Flags().Float64Var(&orchMaxCost, "max-cost", 5.00, "stop when total spend reaches this many USD")
	return cmd
}

func newOrchestratePlanCmd(f *orchFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan <spec-id>",
		Short: "Have a read-only agent propose a task graph for an approved spec (a person still approves it)",
		Long: "Runs one bounded Claude Code session that may read the codebase and submit exactly one thing: `acline plan propose`.\n" +
			"It cannot edit files, and it cannot approve, edit or reject a plan: what it produces is a draft in `acline plan show`,\n" +
			"and nothing becomes a task until a person runs `acline plan approve`. Only a spec that is approved and has no plan\n" +
			"or tasks is planned. Same bounds and rules as `orchestrate step` (agent identity, approval token required to launch,\n" +
			"spend cap, timeout, stop file).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specID, err := parseID(args[0], "spec")
			if err != nil {
				return err
			}
			if !f.dryRun {
				if err := f.preflight(); err != nil {
					return err
				}
			}
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			f.asAgent()
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			r, err := orchestrate.Plan(ctx, f.c.st, orchestrate.ClaudeAgent{Bin: f.agentBin}, orchestrate.PlanOptions{
				SpecID: specID, StepBudgetUSD: f.stepBudget, StepTimeout: f.timeout, Dir: dir,
				ExtraAllow: f.allowTools, PassEnv: f.passEnv, StopFile: f.stopFile(), DryRun: f.dryRun, NoSandbox: f.noSandbox,
			})
			if err != nil {
				return err
			}
			fmt.Printf("spec #%d\n", r.SpecID)
			if r.Launched {
				fmt.Printf("  ran %s, exit %d", r.Duration.Round(time.Second), r.ExitCode)
				if r.TimedOut {
					fmt.Print(", timed out")
				}
				fmt.Printf(", cost $%.2f\n", r.CostUSD)
			}
			if r.Summary != "" {
				fmt.Printf("  agent said: %s\n", strings.ReplaceAll(r.Summary, "\n", "\n              "))
			}
			for _, d := range r.Denied {
				fmt.Printf("  permission denied: %s\n", d)
			}
			if len(r.Command) > 0 {
				fmt.Printf("  would run: %s\n", strings.Join(r.Command, " "))
			}
			if f.showPrompt && r.Prompt != "" {
				fmt.Printf("\n----- prompt -----\n%s\n----- end prompt -----\n", r.Prompt)
			}
			if r.PlanID != 0 {
				fmt.Printf("  proposed plan #%d — a person reviews it: acline plan show %d\n", r.PlanID, r.PlanID)
			}
			if r.Stop != orchestrate.StopNone {
				fmt.Printf("  stopped: %s — %s\n", r.Stop, r.Detail)
			}
			return nil
		},
	}
	return cmd
}

func newOrchestrateSpecCmd(f *orchFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spec <idea...>",
		Short: "Have a read-only agent draft a spec from an idea (a person still approves it)",
		Long: "Runs one bounded Claude Code session that may read the codebase and submit exactly one thing: `acline spec add`.\n" +
			"It cannot edit files, and it cannot approve or revise a spec: what it produces is a draft in `acline spec show`,\n" +
			"and nothing derives from it (no plan, no task) until a person runs `acline spec approve`. Same bounds and rules as\n" +
			"`orchestrate step`/`plan` (agent identity, approval token required to launch, spend cap, timeout, stop file).",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			idea := strings.Join(args, " ")
			if !f.dryRun {
				if err := f.preflight(); err != nil {
					return err
				}
			}
			projectID, err := f.c.resolveProjectFlagOptional(f.project)
			if err != nil {
				return err
			}
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			f.asAgent()
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			r, err := orchestrate.DraftSpec(ctx, f.c.st, orchestrate.ClaudeAgent{Bin: f.agentBin}, orchestrate.SpecOptions{
				Idea: idea, ProjectID: projectID, StepBudgetUSD: f.stepBudget, StepTimeout: f.timeout, Dir: dir,
				ExtraAllow: f.allowTools, PassEnv: f.passEnv, StopFile: f.stopFile(), DryRun: f.dryRun, NoSandbox: f.noSandbox,
			})
			if err != nil {
				return err
			}
			if r.Launched {
				fmt.Printf("ran %s, exit %d", r.Duration.Round(time.Second), r.ExitCode)
				if r.TimedOut {
					fmt.Print(", timed out")
				}
				fmt.Printf(", cost $%.2f\n", r.CostUSD)
			}
			if r.Summary != "" {
				fmt.Printf("agent said: %s\n", strings.ReplaceAll(r.Summary, "\n", "\n            "))
			}
			for _, d := range r.Denied {
				fmt.Printf("permission denied: %s\n", d)
			}
			if len(r.Command) > 0 {
				fmt.Printf("would run: %s\n", strings.Join(r.Command, " "))
			}
			if f.showPrompt && r.Prompt != "" {
				fmt.Printf("\n----- prompt -----\n%s\n----- end prompt -----\n", r.Prompt)
			}
			if r.SpecID != 0 {
				fmt.Printf("proposed spec #%d — a person reviews it: acline spec show %d\n", r.SpecID, r.SpecID)
			}
			if r.Stop != orchestrate.StopNone {
				fmt.Printf("stopped: %s — %s\n", r.Stop, r.Detail)
			}
			return nil
		},
	}
	return cmd
}

func newOrchestrateResearchCmd(f *orchFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "research <question...>",
		Short: "Have a read-only agent research a question using the web and this project (a person still accepts what it finds)",
		Long: "Runs one bounded Claude Code session with read access to the web (WebSearch/WebFetch) and this project. It cannot\n" +
			"edit files, cannot accept a decision, and cannot add a dependency. It submits exactly one of: `acline decision add`\n" +
			"(a draft decision, citing a URL or file for every claim) or `acline note add` when nothing rises to a decision.\n" +
			"Nothing derives from a proposed decision until a person runs `acline decision accept`. This is the first orchestrate\n" +
			"step that reads content acline does not control; see RESEARCH.md. Same bounds and rules otherwise as `orchestrate\n" +
			"step`/`spec` (agent identity, approval token required to launch, spend cap, timeout, stop file).",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			question := strings.Join(args, " ")
			if !f.dryRun {
				if err := f.preflight(); err != nil {
					return err
				}
			}
			projectID, err := f.c.resolveProjectFlagOptional(f.project)
			if err != nil {
				return err
			}
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			f.asAgent()
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			r, err := orchestrate.Research(ctx, f.c.st, orchestrate.ClaudeAgent{Bin: f.agentBin}, orchestrate.ResearchOptions{
				Question: question, ProjectID: projectID, StepBudgetUSD: f.stepBudget, StepTimeout: f.timeout, Dir: dir,
				ExtraAllow: f.allowTools, PassEnv: f.passEnv, StopFile: f.stopFile(), DryRun: f.dryRun, NoSandbox: f.noSandbox,
			})
			if err != nil {
				return err
			}
			if r.Launched {
				fmt.Printf("ran %s, exit %d", r.Duration.Round(time.Second), r.ExitCode)
				if r.TimedOut {
					fmt.Print(", timed out")
				}
				fmt.Printf(", cost $%.2f\n", r.CostUSD)
			}
			if r.Summary != "" {
				fmt.Printf("agent said: %s\n", strings.ReplaceAll(r.Summary, "\n", "\n            "))
			}
			for _, d := range r.Denied {
				fmt.Printf("permission denied: %s\n", d)
			}
			if len(r.Command) > 0 {
				fmt.Printf("would run: %s\n", strings.Join(r.Command, " "))
			}
			if f.showPrompt && r.Prompt != "" {
				fmt.Printf("\n----- prompt -----\n%s\n----- end prompt -----\n", r.Prompt)
			}
			if r.DecisionID != 0 {
				fmt.Printf("proposed decision #%d — a person reviews it: acline decision show %d\n", r.DecisionID, r.DecisionID)
			}
			if r.NoteID != 0 {
				fmt.Printf("recorded note #%d (no decision was warranted)\n", r.NoteID)
			}
			if r.Stop != orchestrate.StopNone {
				fmt.Printf("stopped: %s — %s\n", r.Stop, r.Detail)
			}
			return nil
		},
	}
	return cmd
}

func newOrchestrateStopCmd(f *orchFlags) *cobra.Command {
	var (
		orchStopClear bool
	)
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop any orchestrator run before its next launch (--clear to allow launching again)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := f.stopFile()
			if orchStopClear {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				fmt.Println("stop cleared")
				return nil
			}
			if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
				return err
			}
			fmt.Printf("stop set (%s): no further steps will launch. A step already running finishes; press Ctrl-C in its terminal to end it now.\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&orchStopClear, "clear", false, "remove the stop marker")
	return cmd
}
