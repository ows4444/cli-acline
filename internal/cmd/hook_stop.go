package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

// The Stop hook runs at the end of every agent turn, where SessionEnd runs once.
// It does two things:
//
//   - records the turn's MEMORY_LOG lines, sharing PreCompact/SessionEnd's state
//     so none is recorded twice, and a crash later in the session loses nothing;
//   - blocks the stop once when the active task's passing checks are about older
//     code: acline ran them, the tree has changed since, and nothing has passed
//     against the tree as it is now. That is the "re-run after your last edit"
//     rule, caught before the turn ends instead of at `task done`.
//
// Unlike pre-tool-use it fails open: it is a reminder, not a security boundary, so
// any error, an unrelated session or a slow tree hash lets the turn end.
// stop_hook_active (set when the agent is already continuing because of a Stop
// hook) also lets it end, so the hook can never loop.

// stopTreeTimeout bounds the working-tree hash; past it the turn ends unblocked.
var stopTreeTimeout = 5 * time.Second

// stopTree fingerprints the working tree; a variable so tests can fix it.
var stopTree = currentTree

var hookStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop: record MEMORY_LOG lines, and flag checks that predate the agent's last edit",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStop(os.Stdin, os.Stdout, enterProjectDir())
	},
}

func runStop(in io.Reader, out io.Writer, projectDir string) error {
	var payload struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		StopHookActive bool   `json:"stop_hook_active"`
	}
	_ = json.NewDecoder(in).Decode(&payload)
	sessionID := payload.SessionID
	if sessionID == "" {
		sessionID = "unknown"
	}
	state := filepath.Join(projectDir, ".claude", "vault", ".state", "memory-log-captured.json")
	captureMemoryLog(sessionID, payload.TranscriptPath, state, projectDir)

	if payload.StopHookActive {
		return nil
	}
	reason := staleCheckReason(st)
	if reason == "" {
		return nil
	}
	return json.NewEncoder(out).Encode(map[string]any{"decision": "block", "reason": reason})
}

// staleCheckReason says why the turn should not end yet, or "" to let it end.
func staleCheckReason(s *store.Store) string {
	if s == nil {
		return ""
	}
	sess, err := s.CurrentSession()
	if err != nil || !sess.TaskID.Valid || !sameProject(s, sess) {
		return ""
	}
	task, err := s.GetTask(sess.TaskID.Int64)
	if err != nil || task.Status == "done" || task.Status == "cancelled" {
		return ""
	}
	checks, err := s.ListChecks(task.ID)
	if err != nil {
		return ""
	}
	// Newest result per kind, as the gate reads them.
	latest := map[string]store.Check{}
	for _, c := range checks {
		latest[c.Kind] = c
	}
	var trees []store.Check
	for _, c := range latest {
		if c.Status == "pass" && c.Source == store.CheckSourceRunner && c.TreeHash.Valid {
			trees = append(trees, c)
		}
	}
	if len(trees) == 0 {
		return ""
	}
	now := hashWithin(stopTreeTimeout)
	if now == "" {
		return ""
	}
	var kinds []string
	for _, c := range trees {
		if c.TreeHash.String == now {
			return ""
		}
		kinds = append(kinds, c.Kind)
	}
	sort.Strings(kinds)
	return fmt.Sprintf(
		"acline: task #%d's passing checks (%v) ran against older code; the files changed since. "+
			"Re-run them after your last edit (acline check run %d --kind test, and the others), "+
			"or say plainly why they cannot run. A pass about older code is not evidence.",
		task.ID, kinds, task.ID)
}

// sameProject reports whether the active session belongs to the project this
// hook runs in. Sessions are store-wide, so another project's session must not
// block this one; with no project on either side (a single-project setup) it does.
func sameProject(s *store.Store, sess *store.Session) bool {
	p, err := s.ResolveCurrentProject()
	if err != nil {
		return !sess.ProjectID.Valid
	}
	return sess.ProjectID.Valid && sess.ProjectID.Int64 == p.ID
}

// hashWithin runs stopTree, or returns "" if it takes longer than timeout.
func hashWithin(timeout time.Duration) string {
	tree := stopTree
	done := make(chan string, 1)
	go func() { done <- tree() }()
	select {
	case h := <-done:
		return h
	case <-time.After(timeout):
		return ""
	}
}

func init() {
	hookCmd.AddCommand(hookStopCmd)
}
