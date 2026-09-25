package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// columnsOf returns the set of column names PRAGMA table_info reports for
// table, so tests can assert on schema shape without depending on a
// specific driver's error text for "no such column".
func columnsOf(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return cols
}

func TestEnsureColumnAddsMissingColumn(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "raw.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureColumn(tx, "widgets", "project_id INTEGER REFERENCES projects(id)"); err != nil {
		t.Fatalf("ensureColumn: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if cols := columnsOf(t, db, "widgets"); !cols["project_id"] {
		t.Fatalf("expected widgets to gain a project_id column, got columns %v", cols)
	}
}

func TestEnsureColumnNoopWhenColumnAlreadyExists(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "raw.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE widgets (id INTEGER PRIMARY KEY, project_id INTEGER)`); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// Must not attempt a second ALTER TABLE ADD COLUMN, which SQLite
	// rejects outright ("duplicate column name") — that's the entire
	// reason ensureColumn checks PRAGMA table_info first.
	if err := ensureColumn(tx, "widgets", "project_id INTEGER REFERENCES projects(id)"); err != nil {
		t.Fatalf("expected a no-op for an already-present column, got: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureColumnNoopWhenTableMissing(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "raw.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// A table a migration lists that doesn't exist on this db isn't an
	// error: schema's own CREATE TABLE IF NOT EXISTS will have created it
	// fresh, with the column already in place, before migrations ever run.
	if err := ensureColumn(tx, "does_not_exist", "project_id INTEGER"); err != nil {
		t.Fatalf("expected a no-op for a nonexistent table, got: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestOpenRecordsSchemaVersion locks in that a fresh db (created entirely by
// Open's own schema exec, which already includes every column) still ends
// up at the current schemaVersion — migrations must be a no-op, not a
// no-run, on a db that already satisfies them.
func TestOpenRecordsSchemaVersion(t *testing.T) {
	s := humanStore(t)
	var version int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("expected PRAGMA user_version = %d after Open, got %d", schemaVersion, version)
	}
}

// TestOpenMigratesLegacyDecisionsTable is the scenario the "Migration story"
// describes: an install
// whose `decisions` table was created before project_id existed in schema.
// CREATE TABLE IF NOT EXISTS alone would leave that table exactly as it
// was forever; Open() must also run the ALTER TABLE migration so the
// existing, real data becomes project-scopable without a manual fix.
func TestOpenMigratesLegacyDecisionsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Mirrors the current `decisions` CREATE TABLE in schema, minus
	// project_id — i.e. the table shape before that column was added.
	if _, err := raw.Exec(`
		CREATE TABLE decisions (
			id            INTEGER PRIMARY KEY,
			title         TEXT NOT NULL,
			status        TEXT NOT NULL DEFAULT 'proposed',
			scope         TEXT,
			superseded_by INTEGER REFERENCES decisions(id),
			context       TEXT,
			decision      TEXT,
			rationale     TEXT,
			actor_type    TEXT,
			actor_id      TEXT,
			model         TEXT,
			created_at    TEXT NOT NULL,
			updated_at    TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO decisions (title, status, created_at, updated_at) VALUES (?, 'accepted', ?, ?)`,
		"pre-existing decision from before project scoping", "2020-01-01T00:00:00Z", "2020-01-01T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a legacy-shaped db: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if cols := columnsOf(t, s.DB, "decisions"); !cols["project_id"] {
		t.Fatalf("expected decisions to gain project_id via migration, got columns %v", cols)
	}

	// The pre-existing row must survive the migration untouched (NULL
	// project_id — unscoped, not deleted or reset).
	unscoped, err := s.ListDecisions("", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range unscoped {
		if d.Title == "pre-existing decision from before project scoping" {
			found = true
			if d.ProjectID.Valid {
				t.Fatalf("expected the pre-existing row's project_id to be NULL after migration, got %v", d.ProjectID)
			}
		}
	}
	if !found {
		t.Fatal("expected the pre-existing decision to survive the migration")
	}

	// And the newly-migrated column must actually work end to end: a new,
	// project-scoped decision must round-trip and isolate correctly
	// alongside the untouched legacy row.
	if _, err := s.AddProject("demo", "/tmp/demo", "hotl"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDecision("new scoped decision", DecisionOpts{ProjectID: &p.ID}); err != nil {
		t.Fatalf("AddDecision after migration: %v", err)
	}
	scoped, err := s.ListDecisions("", &p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].Title != "new scoped decision" {
		t.Fatalf("expected exactly the new project-scoped decision, got %+v", scoped)
	}

	allAfter, err := s.ListDecisions("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(allAfter) != 2 {
		t.Fatalf("expected the legacy row plus the new one in an unscoped list, got %d", len(allAfter))
	}
}

// TestOpenMigratesLegacySessionsTable covers migration 2 (project_id added
// to sessions, for the "session <-> project <-> actor not linked" finding):
// an install whose sessions table predates that column must gain it, and
// its pre-existing session must survive as unscoped (project_id NULL), not
// be deleted or reset.
func TestOpenMigratesLegacySessionsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-sessions.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Mirrors the current `sessions` CREATE TABLE in schema, minus project_id.
	if _, err := raw.Exec(`
		CREATE TABLE sessions (
			id         INTEGER PRIMARY KEY,
			task_id    INTEGER REFERENCES tasks(id),
			actor_type TEXT,
			actor_id   TEXT,
			model      TEXT,
			policy     TEXT,
			tokens_in  INTEGER NOT NULL DEFAULT 0,
			tokens_out INTEGER NOT NULL DEFAULT 0,
			cost_usd   REAL NOT NULL DEFAULT 0,
			started_at TEXT NOT NULL,
			ended_at   TEXT,
			summary    TEXT
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO sessions (actor_type, actor_id, started_at) VALUES ('human', 'tester', '2020-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a legacy-shaped sessions table: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if cols := columnsOf(t, s.DB, "sessions"); !cols["project_id"] {
		t.Fatalf("expected sessions to gain project_id via migration, got columns %v", cols)
	}

	legacy, err := s.ListSessions(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy) != 1 || legacy[0].ProjectID.Valid {
		t.Fatalf("expected the pre-existing session to survive as unscoped, got %+v", legacy)
	}

	if _, err := s.AddProject("demo", "/tmp/demo", "hotl"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartSession(nil, &p.ID, nil, ""); err != nil {
		t.Fatalf("StartSession after migration: %v", err)
	}

	scoped, err := s.ListSessions(&p.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 {
		t.Fatalf("expected exactly the new project-scoped session, got %+v", scoped)
	}

	all, err := s.ListSessions(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected the legacy session plus the new one in an unscoped list, got %d", len(all))
	}
}

// TestOpenMigrationIsIdempotentAcrossReopens confirms a second Open() on the
// same db (the normal case — every CLI invocation opens the db fresh) does
// not re-run migration 1's ALTER TABLE and fail with "duplicate column".
func TestOpenMigrationIsIdempotentAcrossReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open on an already-migrated db: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	var version int
	if err := s2.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("expected version to remain %d after reopening, got %d", schemaVersion, version)
	}
}

// TestOpenMigratesLegacyDependenciesAndEvalsTables covers migration 3
// (project_id added to dependencies/evals): an install whose tables predate that column must gain it,
// and each pre-existing row must survive as unscoped, not be deleted or
// reset.
func TestOpenMigratesLegacyDependenciesAndEvalsTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-deps-evals.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE dependencies (
			id         INTEGER PRIMARY KEY,
			task_id    INTEGER REFERENCES tasks(id),
			ecosystem  TEXT NOT NULL,
			name       TEXT NOT NULL,
			version    TEXT,
			verified   INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE evals (
			id         INTEGER PRIMARY KEY,
			task_id    INTEGER REFERENCES tasks(id),
			suite      TEXT NOT NULL,
			pass_rate  REAL NOT NULL,
			sample_size INTEGER,
			note       TEXT,
			actor_type TEXT,
			actor_id   TEXT,
			created_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO dependencies (ecosystem, name, verified, created_at) VALUES ('npm', 'left-pad', 0, '2020-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO evals (suite, pass_rate, created_at) VALUES ('legacy-suite', 0.5, '2020-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on legacy-shaped dependencies/evals tables: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if cols := columnsOf(t, s.DB, "dependencies"); !cols["project_id"] {
		t.Fatalf("expected dependencies to gain project_id via migration, got columns %v", cols)
	}
	if cols := columnsOf(t, s.DB, "evals"); !cols["project_id"] {
		t.Fatalf("expected evals to gain project_id via migration, got columns %v", cols)
	}

	legacyDeps, err := s.ListDependencies(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyDeps) != 1 || legacyDeps[0].ProjectID.Valid {
		t.Fatalf("expected the pre-existing dependency to survive as unscoped, got %+v", legacyDeps)
	}
	legacyEvals, err := s.ListEvals(nil, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyEvals) != 1 || legacyEvals[0].ProjectID.Valid {
		t.Fatalf("expected the pre-existing eval to survive as unscoped, got %+v", legacyEvals)
	}

	if _, err := s.AddProject("demo", "/tmp/demo", "hotl"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDependency(nil, &p.ID, "npm", "right-pad", "", false); err != nil {
		t.Fatalf("AddDependency after migration: %v", err)
	}
	if _, err := s.AddEval(nil, &p.ID, "legacy-suite", 0.9, 0, ""); err != nil {
		t.Fatalf("AddEval after migration: %v", err)
	}

	scopedDeps, err := s.ListDependencies(&p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopedDeps) != 1 || scopedDeps[0].Name != "right-pad" {
		t.Fatalf("expected exactly the new project-scoped dependency, got %+v", scopedDeps)
	}
	scopedEvals, err := s.ListEvals(&p.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopedEvals) != 1 {
		t.Fatalf("expected exactly the new project-scoped eval, got %+v", scopedEvals)
	}

	allDeps, err := s.ListDependencies(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(allDeps) != 2 {
		t.Fatalf("expected the legacy dependency plus the new one in an unscoped list, got %d", len(allDeps))
	}
	allEvals, err := s.ListEvals(nil, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(allEvals) != 2 {
		t.Fatalf("expected the legacy eval plus the new one in an unscoped list, got %d", len(allEvals))
	}
}

// TestOpenMigratesLegacyTablesForRoles is migration 4 (acline spec #2,
// "roles"): a pre-roles db (tasks/checks with no role_id, approvals with no
// actor_type/actor_id/model/role_id at all) gains those columns, gains the
// roles table seeded with the 5 global roles exactly once, and every
// pre-existing row survives with role_id NULL -- so the roles feature
// changes nothing about a project that hasn't touched it.
func TestOpenMigratesLegacyTablesForRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-roles.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE tasks (
			id              INTEGER PRIMARY KEY,
			title           TEXT NOT NULL,
			description     TEXT,
			status          TEXT NOT NULL DEFAULT 'todo',
			priority        TEXT NOT NULL DEFAULT 'normal',
			area            TEXT,
			type            TEXT,
			risk            TEXT NOT NULL DEFAULT 'low',
			autonomy        TEXT NOT NULL DEFAULT 'hotl',
			deferred        INTEGER NOT NULL DEFAULT 0,
			deferred_reason TEXT,
			revisit_trigger TEXT,
			spec_id         INTEGER REFERENCES specs(id),
			decision_id     INTEGER REFERENCES decisions(id),
			project_id      INTEGER REFERENCES projects(id),
			parent_id       INTEGER REFERENCES tasks(id),
			milestone_id    INTEGER REFERENCES milestones(id),
			actor_type      TEXT,
			actor_id        TEXT,
			model           TEXT,
			created_at      TEXT NOT NULL,
			updated_at      TEXT NOT NULL,
			completed_at    TEXT
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE checks (
			id         INTEGER PRIMARY KEY,
			task_id    INTEGER NOT NULL REFERENCES tasks(id),
			kind       TEXT NOT NULL,
			status     TEXT NOT NULL,
			detail     TEXT,
			actor_type TEXT,
			actor_id   TEXT,
			created_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE approvals (
			id         INTEGER PRIMARY KEY,
			task_id    INTEGER NOT NULL REFERENCES tasks(id),
			kind       TEXT NOT NULL,
			approver   TEXT NOT NULL,
			decision   TEXT NOT NULL,
			note       TEXT,
			created_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO tasks (id, title, risk, autonomy, created_at, updated_at) VALUES (1, 'legacy task', 'low', 'hotl', '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO checks (task_id, kind, status, created_at) VALUES (1, 'test', 'pass', '2020-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO approvals (task_id, kind, approver, decision, created_at) VALUES (1, 'code_review', 'alice', 'approved', '2020-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on legacy-shaped tasks/checks/approvals tables: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	for _, table := range []string{"tasks", "checks", "decisions", "specs", "milestones", "sessions", "memory", "notes", "events"} {
		if cols := columnsOf(t, s.DB, table); !cols["role_id"] {
			t.Fatalf("expected %s to gain role_id via migration, got columns %v", table, cols)
		}
	}
	approvalCols := columnsOf(t, s.DB, "approvals")
	for _, col := range []string{"actor_type", "actor_id", "model", "role_id"} {
		if !approvalCols[col] {
			t.Fatalf("expected approvals to gain %s via migration, got columns %v", col, approvalCols)
		}
	}

	roles, err := s.ListRoles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 7 {
		t.Fatalf("expected the 7 seeded global roles, got %d: %+v", len(roles), roles)
	}
	seen := map[string]bool{}
	for _, r := range roles {
		if r.ProjectID.Valid {
			t.Fatalf("expected every seeded role to be global (project_id NULL), got %+v", r)
		}
		seen[r.Name] = true
	}
	for _, want := range []string{"designer", "developer", "qa", "manager", "scrummaster", "architect", "security"} {
		if !seen[want] {
			t.Fatalf("expected a global role named %q, got %+v", want, roles)
		}
	}

	task, err := s.GetTask(1)
	if err != nil {
		t.Fatal(err)
	}
	if task.RoleID.Valid {
		t.Fatalf("expected the pre-existing task to survive with role_id unset, got %+v", task)
	}
	checks, err := s.ListChecks(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].RoleID.Valid {
		t.Fatalf("expected the pre-existing check to survive with role_id unset, got %+v", checks)
	}
	approvals, err := s.ListApprovals(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 1 || approvals[0].RoleID.Valid || approvals[0].Approver != "alice" {
		t.Fatalf("expected the pre-existing approval to survive with role_id unset, got %+v", approvals)
	}

	// Reopening must not re-seed (UNIQUE(project_id, name) + INSERT OR
	// IGNORE, and PRAGMA user_version already records migrations 4-5 as done).
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	roles2, err := s2.ListRoles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles2) != 7 {
		t.Fatalf("expected reopening to leave exactly 7 global roles, got %d", len(roles2))
	}
}

// TestEvaluateGateIgnoresRolesUntilProjectOptsIn is the regression test for
// the bug caught while implementing this: the 5 globally-seeded roles
// include two with can_approve=1 (manager, scrummaster), and an earlier
// version of hasApprovingRole counted those globals, which would have
// turned role enforcement on for every existing project the moment
// migration 4 ran. hasApprovingRole must only count a project's own
// project-scoped can_approve role.
func TestEvaluateGateIgnoresRolesUntilProjectOptsIn(t *testing.T) {
	s := humanStore(t)

	if _, err := s.AddProject("demo", "/tmp/demo-roles", "hotl"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}

	taskID, err := s.AddTask("high risk", "", "normal", TaskOpts{Risk: "high", ProjectID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	runnerCheck(t, s, taskID, "test", "pass", "") // high risk needs a check acline ran

	// A plain --by approval, no role at all -- must still satisfy the gate,
	// exactly like before roles existed, since this project has no
	// project-scoped can_approve role.
	if _, err := s.AddApproval(taskID, "code_review", "alice", "approved", ""); err != nil {
		t.Fatal(err)
	}
	g, err := s.EvaluateGate(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK() {
		t.Fatalf("expected the gate to pass on a plain approval with no roles configured, got blockers: %v", g.Blockers)
	}

	// Now the project opts in with its own can_approve role. A *new* task's
	// gate should require it.
	if _, err := s.AddRole(p.ID, "manager", "human", true, nil, "sign-off"); err != nil {
		t.Fatal(err)
	}
	taskID2, err := s.AddTask("high risk 2", "", "normal", TaskOpts{Risk: "high", ProjectID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	runnerCheck(t, s, taskID2, "test", "pass", "") // high risk needs a check acline ran
	if _, err := s.AddApproval(taskID2, "code_review", "alice", "approved", ""); err != nil {
		t.Fatal(err)
	}
	g2, err := s.EvaluateGate(taskID2)
	if err != nil {
		t.Fatal(err)
	}
	if g2.OK() {
		t.Fatal("expected a roleless approval to be insufficient once the project has a can_approve role")
	}

	managerRole, err := s.GetRoleByName(&p.ID, "manager")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddApprovalWithRole(taskID2, &managerRole.ID, "code_review", "alice", "approved", ""); err != nil {
		t.Fatal(err)
	}
	g3, err := s.EvaluateGate(taskID2)
	if err != nil {
		t.Fatal(err)
	}
	if !g3.OK() {
		t.Fatalf("expected a manager-role approval to satisfy the gate, got blockers: %v", g3.Blockers)
	}
}

// Every scoped list filters on project_id, and every event write looks
// events up by type; both need indexes on a fresh store and a migrated one.
func TestIndexesExistOnFreshAndMigratedStores(t *testing.T) {
	check := func(t *testing.T, s *Store) {
		t.Helper()
		for _, name := range []string{"idx_events_type", "idx_tasks_project", "idx_specs_project", "idx_decisions_project", "idx_memory_project", "idx_milestones_project", "idx_features_project"} {
			var n int
			if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil || n != 1 {
				t.Errorf("index %s: count %d, %v", name, n, err)
			}
		}
	}
	t.Run("fresh", func(t *testing.T) { check(t, humanStore(t)) })
	t.Run("migrated from 12", func(t *testing.T) {
		path := t.TempDir() + "/old.db"
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, ddl := range indexesV13 {
			name := strings.Fields(ddl)[5]
			if _, err := s.DB.Exec(`DROP INDEX ` + name); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.DB.Exec(`PRAGMA user_version = 12`); err != nil {
			t.Fatal(err)
		}
		s.Close()
		s, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		check(t, s)
	})
}
