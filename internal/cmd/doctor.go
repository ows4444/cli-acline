package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"acline/internal/checkrun"
	"acline/internal/orchestrate"
	"acline/internal/store"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the store, the audit trail, the approval token and this project's guard hooks (read-only)",
	Long: "Reports what is healthy, what deserves attention and what is broken: the store file and its permissions, SQLite's own\n" +
		"integrity check, the audit hash chain and the seals on approvals and checks, whether an approval token is enabled,\n" +
		"backups left by migrations, whether this directory's Claude Code settings run the guard on every tool it checks,\n" +
		"which project this directory belongs to, and whether the `claude` binary the orchestrator needs is installed.\n" +
		"It changes nothing. Exit status is non-zero only when something is broken ([fail]); [warn] lines are advice.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if fails := runDoctor(cmd.OutOrStdout()); fails > 0 {
			return fmt.Errorf("%d check(s) failed", fails)
		}
		return nil
	},
}

// doctorReport prints check results and counts the failures.
type doctorReport struct {
	w     io.Writer
	fails int
}

func (r *doctorReport) ok(name, format string, a ...any) {
	fmt.Fprintf(r.w, "[ok]   %-16s %s\n", name, fmt.Sprintf(format, a...))
}
func (r *doctorReport) warn(name, format string, a ...any) {
	fmt.Fprintf(r.w, "[warn] %-16s %s\n", name, fmt.Sprintf(format, a...))
}
func (r *doctorReport) fail(name, format string, a ...any) {
	r.fails++
	fmt.Fprintf(r.w, "[fail] %-16s %s\n", name, fmt.Sprintf(format, a...))
}

// walWarnBytes is how large the write-ahead log may grow before it is worth
// mentioning: a WAL that never checkpoints usually means a reader is stuck open.
const walWarnBytes = 64 << 20

// runDoctor writes the report to w and returns how many checks failed.
func runDoctor(w io.Writer) int {
	r := &doctorReport{w: w}
	doctorStore(r)
	doctorAudit(r)
	doctorAuthority(r)
	doctorProject(r)
	doctorSessions(r)
	doctorRunners(r)
	doctorHooks(r)
	doctorClaude(r)
	fmt.Fprintf(w, "\n%d failure(s)\n", r.fails)
	return r.fails
}

func doctorStore(r *doctorReport) {
	info, err := os.Stat(st.Path)
	if err != nil {
		r.fail("store", "cannot stat %s: %v", st.Path, err)
		return
	}
	r.ok("store", "%s (%s, schema %d)", st.Path, humanBytes(info.Size()), store.SchemaVersion())
	if info.Mode().Perm()&0o077 != 0 {
		r.warn("store perms", "%s is readable by other users (mode %v); it holds specs, decisions and the audit trail: chmod 600", st.Path, info.Mode().Perm())
	}
	if wal, err := os.Stat(st.Path + "-wal"); err == nil && wal.Size() > walWarnBytes {
		r.warn("wal", "write-ahead log is %s: a long-lived reader may be blocking checkpoints", humanBytes(wal.Size()))
	}
	var result string
	if err := st.DB.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		r.fail("integrity", "PRAGMA integrity_check: %v", err)
	} else if result != "ok" {
		r.fail("integrity", "SQLite reports: %s", result)
	} else {
		r.ok("integrity", "SQLite integrity_check ok")
	}
	backups, _ := filepath.Glob(st.Path + ".pre-v*")
	sort.Strings(backups)
	if len(backups) > 0 {
		names := make([]string, len(backups))
		for i, b := range backups {
			names[i] = filepath.Base(b)
		}
		r.ok("backups", "%d left by migrations next to the store: %s", len(backups), strings.Join(names, ", "))
	}
}

func doctorAudit(r *doctorReport) {
	chain, err := st.VerifyChain()
	switch {
	case err != nil:
		r.fail("audit trail", "%v", err)
	case !chain.OK():
		r.fail("audit trail", "hash chain broken at event #%d: %s", chain.BadID, chain.Reason)
	case chain.Legacy():
		r.warn("audit trail", "%d event(s), hash chain intact, but %d from an older acline are only tolerated (no hash, or no role in the hash); a person can attest them: acline verify --reseal", chain.Checked, chain.PreMarker)
	default:
		r.ok("audit trail", "%d event(s), hash chain intact", chain.Checked)
	}
	rec, err := st.VerifyRecords()
	switch {
	case err != nil:
		r.fail("seals", "%v", err)
	case !rec.OK():
		r.fail("seals", "%s: %s", rec.BadRef, rec.Reason)
	default:
		r.ok("seals", "%d approval/check record(s) match their seals", rec.Checked)
		if rec.Unsealed > 0 {
			r.warn("seals", "%d record(s) predate sealing and cannot be verified", rec.Unsealed)
		}
	}
}

func doctorAuthority(r *doctorReport) {
	on, err := st.ApprovalTokenEnabled()
	switch {
	case err != nil:
		r.fail("approval token", "%v", err)
	case on:
		r.ok("approval token", "enabled: approvals, gate overrides and other privileged actions need it")
	default:
		r.warn("approval token", "not enabled, so identity is self-declared and anything that sets ACLINE_ACTOR_TYPE=human can approve work — run `acline auth init` in a terminal")
	}
	r.ok("acting as", "%s/%s", st.Actor.Type, st.Actor.ID)
}

func doctorProject(r *doctorReport) {
	projects, err := st.ListProjects()
	if err != nil {
		r.fail("project", "%v", err)
		return
	}
	// Every registered path is inside the guard's write scope; one this broad
	// makes the scope meaningless.
	for _, pr := range projects {
		if pr.Path.Valid && pr.Path.String != "" && store.ProjectPathTooBroad(pr.Path.String) {
			r.fail("project root", "project %q is registered at %s, which is the filesystem root or contains your home directory, so the guard lets agents write almost anywhere — re-register it at the repository's own path", pr.Name, pr.Path.String)
		}
	}
	p, err := st.ResolveCurrentProject()
	switch {
	case err == nil:
		r.ok("project", "this directory belongs to %q", p.Name)
	case errors.Is(err, store.ErrNoProject) && len(projects) >= 2:
		r.warn("project", "no project resolves here, and the store holds %d: commands run unscoped, which shows every project's decisions, specs, memory and tasks — register this directory (`acline init`) or use --project", len(projects))
	case errors.Is(err, store.ErrNoProject):
		r.ok("project", "no project registered here (unscoped, which is fine for a single-project store)")
	default:
		r.fail("project", "%v", err)
	}
}

// staleSessionAge is how long an active session may go without activity
// before doctor reports it as probably abandoned.
const staleSessionAge = 12 * time.Hour

// doctorSessions reports active sessions nothing has happened in for a long
// time (usually a crashed agent). They are not ended here: ending one lifts its
// policy and role, which a person decides.
func doctorSessions(r *doctorReport) {
	stale, err := st.StaleSessions(staleSessionAge)
	if err != nil {
		r.warn("sessions", "%v", err)
		return
	}
	if len(stale) == 0 {
		r.ok("sessions", "no abandoned sessions")
		return
	}
	var lines []string
	for _, sess := range stale {
		lines = append(lines, fmt.Sprintf("#%d started %s by %s", sess.ID, sess.StartedAt, sess.ActorID.String))
	}
	r.warn("sessions", "%d active session(s) with no activity for over %s — if abandoned, end them yourself (`acline session end`, from the project's directory):\n      %s",
		len(stale), staleSessionAge, strings.Join(lines, "\n      "))
}

// doctorRunners suggests runner commands for a project whose ecosystem has no
// built-in default. It never sets one: a runner decides what counts as evidence.
func doctorRunners(r *doctorReport) {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	eco, suggestions := checkrun.SuggestRunners(dir)
	if eco == "" {
		return
	}
	var configured map[string]bool
	if p, err := st.ResolveCurrentProject(); err == nil {
		configured = map[string]bool{}
		if runners, err := st.ListCheckRunners(p.ID); err == nil {
			for _, rn := range runners {
				configured[rn.Kind] = true
			}
		}
	}
	var missing []string
	for _, sg := range suggestions {
		if !configured[sg.Kind] {
			missing = append(missing, fmt.Sprintf("acline check runner set %s %s", sg.Kind, sg.Command))
		}
	}
	if len(missing) == 0 {
		r.ok("check runners", "%s project with its runners configured", eco)
		return
	}
	if configured == nil {
		r.warn("check runners", "%s project, but this directory is not a registered project, so `check run` has no runners and records skipped (which blocks high-risk tasks) — register it, then review and run:\n      %s", eco, strings.Join(missing, "\n      "))
		return
	}
	r.warn("check runners", "%s project with no runner for some kinds, so `check run` records skipped for them (which blocks high-risk tasks) — review and run:\n      %s", eco, strings.Join(missing, "\n      "))
}

func doctorHooks(r *doctorReport) {
	if runtime.GOOS == "windows" {
		r.warn("platform", "the Claude Code hooks are not supported on Windows (the guard needs a POSIX shell for `|| exit 2`, and `auth` reads /dev/tty); run acline under WSL")
	}
	dir, err := os.Getwd()
	if err != nil {
		r.warn("guard hooks", "cannot read the working directory: %v", err)
		return
	}
	tools := make([]string, 0, len(requiredGuardTools()))
	for name := range requiredGuardTools() {
		tools = append(tools, name)
	}
	if err := orchestrate.PreflightHooks(dir, tools); err != nil {
		r.warn("guard hooks", "%v", err)
		return
	}
	r.ok("guard hooks", "%s runs the guard on every tool it checks", filepath.Join(dir, ".claude", "settings.json"))
}

func doctorClaude(r *doctorReport) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		r.warn("claude", "the `claude` binary is not on PATH (only `acline orchestrate` needs it)")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		r.warn("claude", "%s found but `--version` failed: %v", bin, err)
		return
	}
	r.ok("claude", "%s (%s)", bin, strings.TrimSpace(string(out)))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
