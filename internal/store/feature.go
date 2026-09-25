package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Feature struct {
	ID            int64
	Name          string
	Status        string
	OwnerArea     sql.NullString
	SourcePointer sql.NullString
	Description   sql.NullString
	ProjectID     sql.NullInt64
	CreatedAt     string
	UpdatedAt     string
}

var ValidFeatureStatuses = map[string]bool{
	"live": true, "deprecated": true, "removed": true, "n_a": true,
}

type FeatureOpts struct {
	OwnerArea     string
	SourcePointer string
	Description   string
	ProjectID     *int64
}

func (s *Store) AddFeature(name, status string, opts FeatureOpts) (int64, error) {
	if status == "" {
		status = "live"
	}
	if !ValidFeatureStatuses[status] {
		return 0, fmt.Errorf("invalid feature status %q", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`INSERT INTO features (name, status, owner_area, source_pointer, description, project_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		name, status, nullStr(opts.OwnerArea), nullStr(opts.SourcePointer), nullStr(opts.Description), nullInt(opts.ProjectID), now, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SetFeatureStatus(id int64, status string) error {
	if !ValidFeatureStatuses[status] {
		return fmt.Errorf("invalid feature status %q", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE features SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	if err != nil {
		return err
	}
	// Previously a missing id "succeeded" silently.
	return mustExist(res, "feature", id)
}

func (s *Store) ListFeatures(status string, projectID *int64) ([]Feature, error) {
	q := `SELECT id, name, status, owner_area, source_pointer, description, project_id, created_at, updated_at FROM features`
	var where []string
	var args []any
	if status != "" {
		where = append(where, `status = ?`)
		args = append(args, status)
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
	var out []Feature
	for rows.Next() {
		var f Feature
		if err := rows.Scan(&f.ID, &f.Name, &f.Status, &f.OwnerArea, &f.SourcePointer, &f.Description, &f.ProjectID, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
