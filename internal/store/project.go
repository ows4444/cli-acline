package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Project is a tracked repo. Everything else that carries a project_id
// (specs, decisions, memory, milestones, features, tasks, notes) is scoped
// to one of these, or to none (project_id NULL — the pre-multi-project
// behavior, kept as the default so a single-repo setup needs no `project`
// commands at all).
type Project struct {
	ID              int64
	Name            string
	Path            sql.NullString
	AutonomyDefault string
	CreatedAt       string
}

const marker = ".acline-project"

// ErrAgentCannotRegisterProjectPath is returned when an agent registers a
// project with a path. Every registered path is inside the guard's write scope,
// so registering one is deciding where agents may write: a person's call.
var ErrAgentCannotRegisterProjectPath = errors.New("an agent cannot register a project path: a registered path widens where agents may write, so a person must add it")

// ErrProjectPathTooBroad is returned for `/`, the home directory, or any
// directory that contains it: as a write root, that is nearly everything.
var ErrProjectPathTooBroad = errors.New("project path is too broad: the filesystem root, your home directory, or a directory containing it")

// AddProject registers a project. See AddProjectWithToken.
func (s *Store) AddProject(name, path, autonomy string) (int64, error) {
	return s.AddProjectWithToken(name, path, autonomy, "")
}

// AddProjectWithToken registers a project. A project with a path needs a person
// (or the approval token, when one is enabled), since the path becomes part of
// the guard's write scope; a path-less project widens nothing and is open to
// everyone. Either way the path must not be the root or contain the home
// directory, and the registration is recorded as a project_added event.
func (s *Store) AddProjectWithToken(name, path, autonomy, token string) (int64, error) {
	if name == "" {
		return 0, fmt.Errorf("project name is required")
	}
	if autonomy == "" {
		autonomy = "hotl"
	}
	if !ValidAutonomy[autonomy] {
		return 0, fmt.Errorf("invalid autonomy %q", autonomy)
	}
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return 0, fmt.Errorf("resolving project path: %w", err)
		}
		path = abs
		if ProjectPathTooBroad(path) {
			return 0, fmt.Errorf("%s: %w", path, ErrProjectPathTooBroad)
		}
		if err := s.requirePerson(token, ErrAgentCannotRegisterProjectPath); err != nil {
			return 0, err
		}
	}
	sessionID := s.currentSessionID()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`INSERT INTO projects (name, path, autonomy_default, created_at) VALUES (?, ?, ?, ?)`,
		name, nullStr(path), autonomy, now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	msg := fmt.Sprintf("project #%d %q registered", id, name)
	if path != "" {
		msg += " at " + path
	}
	if _, err := s.logEventTx(tx, nil, sessionID, nil, "project_added", msg); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// ProjectPathTooBroad reports whether path is the filesystem root or contains
// the home directory (including being it). `doctor` uses it to flag roots
// registered before registration refused them.
func ProjectPathTooBroad(path string) bool {
	c := canonicalPath(path)
	if c == filepath.Dir(c) { // the root of its volume
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return PathInside(home, c)
	}
	return false
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.DB.Query(`SELECT id, name, path, autonomy_default, created_at FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.AutonomyDefault, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProjectByName(name string) (*Project, error) {
	row := s.DB.QueryRow(`SELECT id, name, path, autonomy_default, created_at FROM projects WHERE name = ?`, name)
	var p Project
	if err := row.Scan(&p.ID, &p.Name, &p.Path, &p.AutonomyDefault, &p.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("project %q not found: %w", name, ErrProjectNotFound)
		}
		return nil, err
	}
	return &p, nil
}

// ResolveCurrentProject tries, in order: the ACLINE_PROJECT env var, then
// whatever ResolveProjectForPath finds from cwd (a `.acline-project` marker
// file walking up, then cwd or an ancestor matching a registered project's
// path). Returns ErrNoProject if none apply — the normal case for a
// single-project setup that never registered one. A name from the env var
// that doesn't exist in this db (ErrProjectNotFound) falls through to the
// path-based resolution rather than hard-failing — see ErrProjectNotFound's
// doc comment for why.
func (s *Store) ResolveCurrentProject() (*Project, error) {
	if name := os.Getenv("ACLINE_PROJECT"); name != "" {
		p, err := s.GetProjectByName(name)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, ErrProjectNotFound) {
			return nil, err
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return s.ResolveProjectForPath(cwd)
}

// ResolveProjectForPath applies ResolveCurrentProject's marker-file-then-
// path-match logic to an arbitrary directory instead of the process's own
// cwd -- the authoritative implementation an MCP client (e.g. the VS Code
// extension, resolving a workspace folder) should call over MCP instead of
// reimplementing path matching itself (a client-side copy once diverged on case
// sensitivity and had no marker-file support). Deliberately does not consult
// ACLINE_PROJECT: that env var expresses the ambient project for this
// whole process, not "the project that owns this specific path."
func (s *Store) ResolveProjectForPath(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	// A marker names a project, but anyone who can put a file in a repo can write
	// one, and the project it names then supplies its decisions and memory as
	// session context. So it is only trusted where that project says it may be:
	// the directory holding the marker must be the project's registered path or
	// inside it. A project registered without a path has nothing to compare
	// against and keeps the old behaviour.
	if name, markerDir, ok := findMarker(abs); ok {
		p, err := s.GetProjectByName(name)
		if err == nil {
			if !p.Path.Valid || p.Path.String == "" || PathInside(markerDir, p.Path.String) {
				return p, nil
			}
		} else if !errors.Is(err, ErrProjectNotFound) {
			return nil, err
		}
	}

	projects, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	// Compare on symlink-resolved paths as well as the literal ones, so a
	// project registered as /var/x and reached as /private/var/x (macOS) or via a
	// symlinked checkout still matches.
	for _, start := range uniquePaths(abs, canonicalPath(abs)) {
		d := start
		for {
			for _, p := range projects {
				if !p.Path.Valid {
					continue
				}
				if p.Path.String == d || canonicalPath(p.Path.String) == d {
					proj := p
					return &proj, nil
				}
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	return nil, ErrNoProject
}

// canonicalPath resolves symlinks in path, falling back to the cleaned path when
// it cannot (it does not exist, or is unreadable).
func canonicalPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}

func uniquePaths(paths ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// PathInside reports whether child is parent or lies inside it, comparing
// symlink-resolved paths.
func PathInside(child, parent string) bool {
	c, p := canonicalPath(child), canonicalPath(parent)
	rel, err := filepath.Rel(p, c)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// findMarker walks up from dir looking for a `.acline-project` file and returns
// its (trimmed) contents as the project name, and the directory it was found in.
func findMarker(dir string) (name, foundIn string, ok bool) {
	for {
		p := filepath.Join(dir, marker)
		if b, err := os.ReadFile(p); err == nil {
			if name := strings.TrimSpace(string(b)); name != "" {
				return name, dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

// WriteMarker records name as the current project for dir via a
// `.acline-project` file, so ResolveCurrentProject finds it without relying on
// the path-matching fallback (useful when a project's registered path isn't
// exactly cwd, e.g. a subdirectory of the tracked repo).
func WriteMarker(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, marker), []byte(name+"\n"), 0o644)
}
