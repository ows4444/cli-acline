package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Dependency records a package an agent or human introduced. Given the measured
// rate of hallucinated imports, every added package is a supply-chain event
// worth its own row.
type Dependency struct {
	ID        int64
	TaskID    sql.NullInt64
	ProjectID sql.NullInt64
	Ecosystem string
	Name      string
	Version   sql.NullString
	Verified  bool
	CreatedAt string
}

// ErrAgentCannotVerifyDependency is returned when an agent tries to mark a
// dependency verified. "Verified" says a person confirmed the package exists and
// is the real published artifact (not a hallucinated or typosquatted name); an
// agent vouching for its own choice proves nothing.
var ErrAgentCannotVerifyDependency = errors.New("an agent cannot mark a dependency verified: a person must confirm the package is real")

func (s *Store) AddDependency(taskID, projectID *int64, ecosystem, name, version string, verified bool) (int64, error) {
	return s.AddDependencyWithToken(taskID, projectID, ecosystem, name, version, verified, "")
}

// AddDependencyWithToken is AddDependency with the approval token, needed to
// record a dependency as already verified.
func (s *Store) AddDependencyWithToken(taskID, projectID *int64, ecosystem, name, version string, verified bool, token string) (int64, error) {
	if verified {
		if err := s.requirePerson(token, ErrAgentCannotVerifyDependency); err != nil {
			return 0, err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`INSERT INTO dependencies (task_id, project_id, ecosystem, name, version, verified, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		nullInt(taskID), nullInt(projectID), ecosystem, name, nullStr(version), verified, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) VerifyDependency(id int64) error {
	return s.VerifyDependencyWithToken(id, "")
}

// VerifyDependencyWithToken marks a dependency verified. Refused for an agent
// unless it presents the approval token; needs the token from anyone when one is
// enabled.
func (s *Store) VerifyDependencyWithToken(id int64, token string) error {
	if err := s.requirePerson(token, ErrAgentCannotVerifyDependency); err != nil {
		return err
	}
	res, err := s.DB.Exec(`UPDATE dependencies SET verified = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return mustExist(res, "dependency", id)
}

// ListDependencies returns dependencies, optionally scoped to a project and/or
// restricted to unverified ones.
func (s *Store) ListDependencies(projectID *int64, unverifiedOnly bool) ([]Dependency, error) {
	q := `SELECT id, task_id, project_id, ecosystem, name, version, verified, created_at FROM dependencies`
	var where []string
	var args []any
	if unverifiedOnly {
		where = append(where, `verified = 0`)
	}
	if projectID != nil {
		where = append(where, `project_id = ?`)
		args = append(args, *projectID)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dependency
	for rows.Next() {
		var d Dependency
		var verified int
		if err := rows.Scan(&d.ID, &d.TaskID, &d.ProjectID, &d.Ecosystem, &d.Name, &d.Version, &verified, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Verified = verified != 0
		out = append(out, d)
	}
	return out, rows.Err()
}
