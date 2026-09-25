package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrAgentCannotConfigureRunner is returned when an agent tries to set or
// remove a project's check runner. A runner is a command that `check run`
// later executes on request; if the agent whose work is being verified could
// choose it, the verification would prove nothing (and it would be arbitrary
// command execution). Only a human, or a holder of the approval token, sets one.
var ErrAgentCannotConfigureRunner = errors.New("an agent cannot configure a check runner: a human must set it")

// RunnableCheckKinds are the check kinds `check run` can execute.
var RunnableCheckKinds = map[string]bool{"test": true, "lint": true, "sast": true, "sca": true}

// CheckRunner is a project's command for one check kind.
type CheckRunner struct {
	ProjectID int64
	Kind      string
	Command   string
	ActorID   sql.NullString
	CreatedAt string
}

func (s *Store) authorizeRunnerChange(token string) error {
	viaToken, err := s.authorize(token)
	if err != nil {
		return err
	}
	if s.Actor.Type == "agent" && !viaToken {
		return ErrAgentCannotConfigureRunner
	}
	return nil
}

// SetCheckRunner sets (or replaces) the command `check run` uses for kind in
// a project, overriding the built-in Go default. Human-only; see
// ErrAgentCannotConfigureRunner.
func (s *Store) SetCheckRunner(projectID int64, kind, command, token string) error {
	if !RunnableCheckKinds[kind] {
		return fmt.Errorf("invalid runner kind %q (want: test|lint|sast|sca)", kind)
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("runner command is empty")
	}
	if err := s.authorizeRunnerChange(token); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.writeWithEvent(nil, "check_runner_set", func(tx *sql.Tx) (string, error) {
		_, err := tx.Exec(
			`INSERT INTO check_runners (project_id, kind, command, actor_type, actor_id, created_at) VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT (project_id, kind) DO UPDATE SET command = excluded.command, actor_type = excluded.actor_type,
			 actor_id = excluded.actor_id, created_at = excluded.created_at`,
			projectID, kind, command, s.Actor.Type, s.Actor.ID, now,
		)
		return fmt.Sprintf("project #%d %s runner set: %s", projectID, kind, command), err
	})
}

// UnsetCheckRunner removes a project's override, restoring the default.
func (s *Store) UnsetCheckRunner(projectID int64, kind, token string) error {
	if err := s.authorizeRunnerChange(token); err != nil {
		return err
	}
	return s.writeWithEvent(nil, "check_runner_set", func(tx *sql.Tx) (string, error) {
		res, err := tx.Exec(`DELETE FROM check_runners WHERE project_id = ? AND kind = ?`, projectID, kind)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "", fmt.Errorf("project #%d has no %s runner", projectID, kind)
		}
		return fmt.Sprintf("project #%d %s runner removed", projectID, kind), nil
	})
}

// CheckRunnerCommand returns the project's command for kind, or "" when the
// project has none (or no project): callers then fall back to the default.
func (s *Store) CheckRunnerCommand(projectID *int64, kind string) (string, error) {
	if projectID == nil {
		return "", nil
	}
	var cmd string
	err := s.DB.QueryRow(`SELECT command FROM check_runners WHERE project_id = ? AND kind = ?`, *projectID, kind).Scan(&cmd)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return cmd, err
}

// ListCheckRunners lists a project's configured runners.
func (s *Store) ListCheckRunners(projectID int64) ([]CheckRunner, error) {
	rows, err := s.DB.Query(`SELECT project_id, kind, command, actor_id, created_at FROM check_runners WHERE project_id = ? ORDER BY kind`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckRunner
	for rows.Next() {
		var r CheckRunner
		if err := rows.Scan(&r.ProjectID, &r.Kind, &r.Command, &r.ActorID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
