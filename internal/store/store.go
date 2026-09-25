package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNoProject is returned by ResolveCurrentProject and ResolveProjectForPath
// when neither ACLINE_PROJECT (ResolveCurrentProject only), a
// `.acline-project` marker file, nor the path (or cwd) matching a registered
// project path identifies one. Callers treat this as "operate unscoped"
// (project_id NULL), not as a hard failure.
var ErrNoProject = errors.New("no current project")

// ErrProjectNotFound wraps GetProjectByName's error when the named project
// doesn't exist in the currently-open db. An explicit --project flag should
// still surface this as a hard error (a typo'd name is a real mistake), but
// ResolveCurrentProject's ambient sources (ACLINE_PROJECT, a .acline-project
// marker) treat it as "doesn't apply here" and fall through instead — a
// marker or env var naming a project this particular db doesn't have is not
// meaningfully different from no marker at all, and hard-failing every
// command on it (rather than falling back to unscoped) is surprising and,
// for the specific case of a marker file walking up from cwd into an
// unrelated db — e.g. a test suite's isolated temp db — actively breaks
// things that have nothing to do with project resolution.
var ErrProjectNotFound = errors.New("project not found")

// ErrNotFound is wrapped into every entity lookup's "not found" error (task,
// decision, spec, milestone, role, note, and mustExist's update-path check),
// so a caller can test errors.Is(err, ErrNotFound) once instead of matching
// each entity's specific message text. internal/mcp's error-code middleware
// uses it to attach a stable "not_found" code to the MCP response.
var ErrNotFound = errors.New("not found")

// specVersionsDDL is shared by `schema` (fresh stores) and migration 11, so the
// two cannot drift.
//
// What a spec said before each revision (specs itself holds only the current
// text): revising an approved spec withdraws its approval, and this is how the
// text that was approved stays recoverable. status is the spec's status when
// that version was replaced.
const specVersionsDDL = `CREATE TABLE IF NOT EXISTS spec_versions (
	id         INTEGER PRIMARY KEY,
	spec_id    INTEGER NOT NULL REFERENCES specs(id),
	version    INTEGER NOT NULL,
	title      TEXT NOT NULL,
	body       TEXT,
	status     TEXT NOT NULL,
	actor_type TEXT,
	actor_id   TEXT,
	created_at TEXT NOT NULL,
	UNIQUE (spec_id, version)
);
`

const schema = `
CREATE TABLE IF NOT EXISTS projects (
	id               INTEGER PRIMARY KEY,
	name             TEXT UNIQUE NOT NULL,
	path             TEXT,
	autonomy_default TEXT NOT NULL DEFAULT 'hotl',
	created_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS specs (
	id          INTEGER PRIMARY KEY,
	title       TEXT NOT NULL,
	body        TEXT,
	status      TEXT NOT NULL DEFAULT 'draft',
	version     INTEGER NOT NULL DEFAULT 1,
	project_id  INTEGER REFERENCES projects(id),
	actor_type  TEXT,
	actor_id    TEXT,
	model       TEXT,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS decisions (
	id            INTEGER PRIMARY KEY,
	title         TEXT NOT NULL,
	status        TEXT NOT NULL DEFAULT 'proposed',
	scope         TEXT,
	superseded_by INTEGER REFERENCES decisions(id),
	context       TEXT,
	decision      TEXT,
	rationale     TEXT,
	project_id    INTEGER REFERENCES projects(id),
	actor_type    TEXT,
	actor_id      TEXT,
	model         TEXT,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS milestones (
	id          INTEGER PRIMARY KEY,
	name        TEXT NOT NULL,
	description TEXT,
	status      TEXT NOT NULL DEFAULT 'planned',
	target_date TEXT,
	project_id  INTEGER REFERENCES projects(id),
	actor_type  TEXT,
	actor_id    TEXT,
	model       TEXT,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
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
	blocked_reason  TEXT,
	plan_item_id    INTEGER,
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
);

CREATE TABLE IF NOT EXISTS sessions (
	id         INTEGER PRIMARY KEY,
	task_id    INTEGER REFERENCES tasks(id),
	project_id INTEGER REFERENCES projects(id),
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
);

CREATE TABLE IF NOT EXISTS evals (
	id         INTEGER PRIMARY KEY,
	task_id    INTEGER REFERENCES tasks(id),
	project_id INTEGER REFERENCES projects(id),
	suite      TEXT NOT NULL,
	pass_rate  REAL NOT NULL,
	sample_size INTEGER,
	note       TEXT,
	actor_type TEXT,
	actor_id   TEXT,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY,
	task_id    INTEGER REFERENCES tasks(id),
	session_id INTEGER REFERENCES sessions(id),
	type       TEXT NOT NULL,
	message    TEXT NOT NULL,
	actor_type TEXT,
	actor_id   TEXT,
	model      TEXT,
	created_at TEXT NOT NULL,
	prev_hash  TEXT,
	hash       TEXT
);

CREATE TABLE IF NOT EXISTS approvals (
	id         INTEGER PRIMARY KEY,
	task_id    INTEGER NOT NULL REFERENCES tasks(id),
	kind       TEXT NOT NULL,
	approver   TEXT NOT NULL,
	decision   TEXT NOT NULL,
	note       TEXT,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS checks (
	id         INTEGER PRIMARY KEY,
	task_id    INTEGER NOT NULL REFERENCES tasks(id),
	kind       TEXT NOT NULL,
	status     TEXT NOT NULL,
	detail     TEXT,
	actor_type TEXT,
	actor_id   TEXT,
	created_at TEXT NOT NULL,
	source     TEXT NOT NULL DEFAULT 'manual',
	tree_hash  TEXT
);

CREATE TABLE IF NOT EXISTS dependencies (
	id         INTEGER PRIMARY KEY,
	task_id    INTEGER REFERENCES tasks(id),
	project_id INTEGER REFERENCES projects(id),
	ecosystem  TEXT NOT NULL,
	name       TEXT NOT NULL,
	version    TEXT,
	verified   INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memory (
	id          INTEGER PRIMARY KEY,
	area        TEXT,
	kind        TEXT NOT NULL DEFAULT 'lesson',
	body        TEXT NOT NULL,
	status      TEXT NOT NULL DEFAULT 'pending',
	stale       INTEGER NOT NULL DEFAULT 0,
	project_id  INTEGER REFERENCES projects(id),
	actor_type  TEXT,
	actor_id    TEXT,
	model       TEXT,
	reviewed_at TEXT,
	source_kind TEXT,
	source_id   INTEGER,
	created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS check_runners (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id),
	kind       TEXT NOT NULL,
	command    TEXT NOT NULL,
	actor_type TEXT,
	actor_id   TEXT,
	created_at TEXT NOT NULL,
	UNIQUE (project_id, kind)
);

-- plans, plan_items, plan_edges, plan_criteria: created by migration 10 from
-- planTablesDDL (plan.go), the one definition. Fresh stores run every migration.

CREATE TABLE IF NOT EXISTS features (
	id             INTEGER PRIMARY KEY,
	name           TEXT NOT NULL,
	status         TEXT NOT NULL DEFAULT 'live',
	owner_area     TEXT,
	source_pointer TEXT,
	description    TEXT,
	project_id     INTEGER REFERENCES projects(id),
	created_at     TEXT NOT NULL,
	updated_at     TEXT NOT NULL
);

-- Fast, zero-friction capture (the "MEMORY_LOG:" concept): a raw note lands
-- here first and is promoted into a decision or memory entry only once a
-- human (or acline reflect) decides it's durable — see reflect.go.
CREATE TABLE IF NOT EXISTS notes (
	id           INTEGER PRIMARY KEY,
	project_id   INTEGER REFERENCES projects(id),
	body         TEXT NOT NULL,
	source       TEXT NOT NULL DEFAULT 'manual',
	actor_type   TEXT,
	actor_id     TEXT,
	model        TEXT,
	created_at   TEXT NOT NULL,
	promoted_to  TEXT,
	promoted_id  INTEGER
);

-- Keyword search over everything that carries a body of text. Populated
-- explicitly by each Add*/note-add call (see indexForSearch) rather than via
-- triggers, since several source tables are append-only or update-restricted
-- and a plain insert-time index is simpler to reason about than keeping
-- triggers in sync with those constraints.
CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
	kind UNINDEXED, ref_id UNINDEXED, project_id UNINDEXED, body
);

-- Optional semantic-search companion to search_index, populated only when
-- an embedding provider is configured (VOYAGE_API_KEY) -- see
-- internal/embed and Store.Embedder in embedding.go. Vectors are raw
-- little-endian float32 BLOBs; modernc.org/sqlite has no vector extension,
-- so similarity search is a linear scan in Go (SemanticSearch), which is
-- fine at the row counts a local per-project store holds. model is stored
-- per row (not assumed global) so a provider/model change never mixes
-- incomparable vectors in one similarity scan -- see
-- RowsMissingEmbeddings / the reindex-embeddings command.
CREATE TABLE IF NOT EXISTS embeddings (
	kind       TEXT NOT NULL,
	ref_id     INTEGER NOT NULL,
	project_id INTEGER,
	model      TEXT NOT NULL,
	dims       INTEGER NOT NULL,
	vector     BLOB NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (kind, ref_id)
);
CREATE INDEX IF NOT EXISTS idx_embeddings_model ON embeddings(model);

CREATE TABLE IF NOT EXISTS task_criteria (
	id         INTEGER PRIMARY KEY,
	task_id    INTEGER NOT NULL REFERENCES tasks(id),
	text       TEXT NOT NULL,
	pattern    TEXT,
	done       INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS task_links (
	id              INTEGER PRIMARY KEY,
	task_id         INTEGER NOT NULL REFERENCES tasks(id),
	related_task_id INTEGER NOT NULL REFERENCES tasks(id),
	relation        TEXT NOT NULL, -- depends_on | blocks | related
	created_at      TEXT NOT NULL
);

-- A role a contributor (human or agent) acts as: developer, qa, designer,
-- manager, scrummaster, architect, security, or a project's own custom
-- addition. project_id NULL is a built-in/global role, seeded once by
-- migration 4 (the first five) and migration 5 (architect, security), and
-- usable by every project; project_id set is a project-specific addition
-- alongside them.
-- can_approve gates EvaluateGate's approval requirement (see gate.go) --
-- only meaningful once a project actually has a can_approve role, so a
-- project with none configured sees no behavior change at all.
-- stage_order is advisory only (a suggested next-role hint), never enforced.
CREATE TABLE IF NOT EXISTS roles (
	id          INTEGER PRIMARY KEY,
	project_id  INTEGER REFERENCES projects(id),
	name        TEXT NOT NULL,
	kind        TEXT NOT NULL DEFAULT 'both', -- human | agent | both
	can_approve INTEGER NOT NULL DEFAULT 0,
	stage_order INTEGER,
	description TEXT,
	created_at  TEXT NOT NULL,
	UNIQUE(project_id, name)
);

` + specVersionsDDL + `
-- Small key/value settings for the store itself (currently: the hash of the
-- human approval token, see auth.go). Deliberately NOT part of snapshots.
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_events_task ON events(task_id);
CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id);
CREATE INDEX IF NOT EXISTS idx_criteria_task ON task_criteria(task_id);
CREATE INDEX IF NOT EXISTS idx_links_task ON task_links(task_id);
CREATE INDEX IF NOT EXISTS idx_decisions_status ON decisions(status);
CREATE INDEX IF NOT EXISTS idx_features_status ON features(status);
CREATE INDEX IF NOT EXISTS idx_approvals_task ON approvals(task_id);
CREATE INDEX IF NOT EXISTS idx_checks_task ON checks(task_id);
CREATE INDEX IF NOT EXISTS idx_deps_task ON dependencies(task_id);
CREATE INDEX IF NOT EXISTS idx_evals_task ON evals(task_id);
CREATE INDEX IF NOT EXISTS idx_tasks_milestone ON tasks(milestone_id);
CREATE INDEX IF NOT EXISTS idx_milestones_status ON milestones(status);
CREATE INDEX IF NOT EXISTS idx_notes_project ON notes(project_id);
CREATE INDEX IF NOT EXISTS idx_notes_promoted ON notes(promoted_to);

-- M2: the audit trail is append-only. Corrections are new compensating rows,
-- never edits. Enforced here rather than left to convention.
CREATE TRIGGER IF NOT EXISTS events_no_update
BEFORE UPDATE ON events
BEGIN SELECT RAISE(ABORT, 'events are append-only: record a compensating event instead'); END;

CREATE TRIGGER IF NOT EXISTS events_no_delete
BEFORE DELETE ON events
BEGIN SELECT RAISE(ABORT, 'events are append-only: they cannot be deleted'); END;

CREATE TRIGGER IF NOT EXISTS approvals_no_update
BEFORE UPDATE ON approvals
BEGIN SELECT RAISE(ABORT, 'approvals are append-only: record a new approval instead'); END;

CREATE TRIGGER IF NOT EXISTS approvals_no_delete
BEFORE DELETE ON approvals
BEGIN SELECT RAISE(ABORT, 'approvals are append-only: they cannot be deleted'); END;

CREATE TRIGGER IF NOT EXISTS checks_no_update
BEFORE UPDATE ON checks
BEGIN SELECT RAISE(ABORT, 'checks are append-only: record a new check result instead'); END;

CREATE TRIGGER IF NOT EXISTS checks_no_delete
BEFORE DELETE ON checks
BEGIN SELECT RAISE(ABORT, 'checks are append-only: they cannot be deleted'); END;
`

// schemaVersion is the latest migration's version, stored in SQLite's own
// PRAGMA user_version (no extra table needed — it's a single integer the
// engine already persists in the file header). `CREATE TABLE/INDEX IF NOT
// EXISTS` in schema above only ever adds whole tables/indexes to an existing
// db; it never adds a column to a table that already exists, so any change
// that adds a column to a pre-existing table needs an explicit migration
// here, or an install upgrading from an older schema silently keeps missing
// it. Bump this and append to migrations when that happens.
//
// The same applies to a brand-new table or index added to `schema` above:
// Open skips the schema DDL entirely once user_version == schemaVersion, so
// such a change must also bump this constant (with a no-op migration if there
// is no column work) or existing installs will never create it.
const schemaVersion = 13

// SchemaVersion is the schema version this build reads and writes.
func SchemaVersion() int { return schemaVersion }

type migration struct {
	version int
	apply   func(*sql.Tx) error
}

// migrations run in order, each exactly once per db (tracked by
// PRAGMA user_version), inside its own transaction. Migration 1 covers the
// concrete gap this project already hit once: project_id was added to the
// schema for scoping, but an install whose tables
// were created before that column existed in `schema` above would never
// pick it up on its own, since CREATE TABLE IF NOT EXISTS is a no-op once
// the table exists. ensureColumn is idempotent either way, so this is safe
// to run against a db that already has every column too (the common case:
// every fresh db created by Open()'s own schema exec already has them all).
var migrations = []migration{
	{
		version: 1,
		apply: func(tx *sql.Tx) error {
			projectScoped := map[string]string{
				"specs":      "project_id INTEGER REFERENCES projects(id)",
				"decisions":  "project_id INTEGER REFERENCES projects(id)",
				"milestones": "project_id INTEGER REFERENCES projects(id)",
				"tasks":      "project_id INTEGER REFERENCES projects(id)",
				"memory":     "project_id INTEGER REFERENCES projects(id)",
				"features":   "project_id INTEGER REFERENCES projects(id)",
				"notes":      "project_id INTEGER REFERENCES projects(id)",
			}
			for _, table := range []string{"specs", "decisions", "milestones", "tasks", "memory", "features", "notes"} {
				if err := ensureColumn(tx, table, projectScoped[table]); err != nil {
					return fmt.Errorf("table %s: %w", table, err)
				}
			}
			return nil
		},
	},
	{
		// Sessions were not linked to their project: they
		// had no path to the project they belonged to at all (only to a
		// task, which itself wasn't reliably project-scoped before
		// migration 1). Same idempotent ensureColumn pattern as above, kept
		// as its own migration rather than folded into migration 1 since
		// migration 1 may already have run (and been recorded via
		// PRAGMA user_version) against real dbs by the time this was added —
		// migrations are append-only, never edited after the fact.
		version: 2,
		apply: func(tx *sql.Tx) error {
			return ensureColumn(tx, "sessions", "project_id INTEGER REFERENCES projects(id)")
		},
	},
	{
		// Dependencies and evals were not project-scoped: unlike every other
		// task-adjacent entity, neither
		// table ever got a project_id column — not an intentional
		// exclusion, an oversight caught on a second pass. Same
		// ensureColumn pattern as migrations 1 and 2.
		version: 3,
		apply: func(tx *sql.Tx) error {
			for _, table := range []string{"dependencies", "evals"} {
				if err := ensureColumn(tx, table, "project_id INTEGER REFERENCES projects(id)"); err != nil {
					return fmt.Errorf("table %s: %w", table, err)
				}
			}
			return nil
		},
	},
	{
		// Roles feature (acline spec #2): role_id attribution on every
		// table that already carries actor_type/actor_id, plus closing the
		// one real gap in that attribution model -- approvals previously
		// had none of actor_type/actor_id/model, only a free-text
		// `approver` string. Also seeds the 5 global (project_id NULL)
		// roles exactly once via INSERT OR IGNORE, relying on the
		// UNIQUE(project_id, name) constraint for idempotency so this is
		// safe to run against a db that already has them (e.g. re-running
		// after a partial failure). Every new column is nullable and every
		// new INSERT/check that reads role_id treats it as optional, so a
		// project that never sets a role sees no behavior change --
		// verified in TestRolesMigrationBackwardCompatible.
		version: 4,
		apply: func(tx *sql.Tx) error {
			roleScoped := []string{
				"tasks", "decisions", "specs", "milestones", "sessions",
				"checks", "memory", "notes", "events",
			}
			for _, table := range roleScoped {
				if err := ensureColumn(tx, table, "role_id INTEGER REFERENCES roles(id)"); err != nil {
					return fmt.Errorf("table %s: %w", table, err)
				}
			}
			for _, colDef := range []string{
				"actor_type TEXT",
				"actor_id TEXT",
				"model TEXT",
				"role_id INTEGER REFERENCES roles(id)",
			} {
				if err := ensureColumn(tx, "approvals", colDef); err != nil {
					return fmt.Errorf("table approvals: %w", err)
				}
			}
			now := time.Now().UTC().Format(time.RFC3339)
			seeds := []struct {
				name                   string
				kind                   string
				canApprove, stageOrder any
				description            string
			}{
				{"designer", "both", 0, 1, "Shapes what a task should look like/behave like before implementation."},
				{"developer", "both", 0, 2, "Implements the task."},
				{"qa", "both", 0, 3, "Verifies the task; records checks. Does not approve its own work."},
				{"scrummaster", "human", 1, nil, "Facilitates process; can satisfy the approval gate."},
				{"manager", "human", 1, 4, "Owns sign-off; can satisfy the approval gate."},
			}
			for _, r := range seeds {
				if _, err := tx.Exec(
					`INSERT OR IGNORE INTO roles (project_id, name, kind, can_approve, stage_order, description, created_at)
					 VALUES (NULL, ?, ?, ?, ?, ?, ?)`,
					r.name, r.kind, r.canApprove, r.stageOrder, r.description, now,
				); err != nil {
					return fmt.Errorf("seeding role %s: %w", r.name, err)
				}
			}
			return nil
		},
	},
	{
		// Governance gap (acline CLI refactor review): the 5 seeded roles
		// covered spec->implement->verify->sign-off but nothing owned
		// proposing architecture *before* a spec turns into tasks, or
		// security scanning (checks already accept --kind sast|sca, but no
		// role was ever responsible for them the way qa owns --kind test).
		// Seeded the same idempotent INSERT OR IGNORE way as migration 4,
		// so re-running this migration (or running it against a db that
		// somehow already has these rows) is a no-op.
		//
		// stage_order deliberately ties with an existing stage rather than
		// renumbering designer/developer/qa/manager (1..4, seeded by
		// migration 4): stage_order is advisory only (see NextRoleHint) and
		// migrations are append-only, so shifting already-seeded values
		// would be surprising for anything that observed them between
		// migration 4 and this one. architect ties with designer's stage 1
		// (both are pre-implementation, alphabetically architect sorts
		// first); security ties with qa's stage 3 (both are verification,
		// alphabetically qa sorts first) -- ORDER BY in ListRoles already
		// tie-breaks by name, so this reads naturally without a schema or
		// ordering change.
		version: 5,
		apply: func(tx *sql.Tx) error {
			now := time.Now().UTC().Format(time.RFC3339)
			seeds := []struct {
				name                   string
				kind                   string
				canApprove, stageOrder any
				description            string
			}{
				{"architect", "agent", 0, 1, "Proposes architecture (services, schemas, ADRs) before a spec becomes tasks. Does not implement or approve its own proposals."},
				{"security", "agent", 0, 3, "Owns security checks (check --kind sast|sca) and proposes remediations. Does not approve its own findings."},
			}
			for _, r := range seeds {
				if _, err := tx.Exec(
					`INSERT OR IGNORE INTO roles (project_id, name, kind, can_approve, stage_order, description, created_at)
					 VALUES (NULL, ?, ?, ?, ?, ?, ?)`,
					r.name, r.kind, r.canApprove, r.stageOrder, r.description, now,
				); err != nil {
					return fmt.Errorf("seeding role %s: %w", r.name, err)
				}
			}
			return nil
		},
	},
	{
		// Approval token (auth.go): a key/value `meta` table. New table only, no
		// column work, so it is just CREATE IF NOT EXISTS -- but it must have a
		// migration so already-migrated stores (user_version 5) get it, since
		// Open skips the schema DDL once user_version == schemaVersion.
		version: 6,
		apply: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
			return err
		},
	},
	{
		// Learning loop: memory provenance. source_kind names where an entry
		// came from ("note", "check:<kind>", "rejection") and source_id is the
		// note id or task id it points at. NULL for entries written directly.
		version: 7,
		apply: func(tx *sql.Tx) error {
			for _, col := range []string{"source_kind TEXT", "source_id INTEGER"} {
				if err := ensureColumn(tx, "memory", col); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		// Per-project check runners (runner.go). New table only, but it needs a
		// migration so already-migrated stores get it (Open skips the schema DDL
		// once user_version == schemaVersion).
		version: 8,
		apply: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS check_runners (
				id         INTEGER PRIMARY KEY,
				project_id INTEGER NOT NULL REFERENCES projects(id),
				kind       TEXT NOT NULL,
				command    TEXT NOT NULL,
				actor_type TEXT,
				actor_id   TEXT,
				created_at TEXT NOT NULL,
				UNIQUE (project_id, kind)
			)`)
			return err
		},
	},
	{
		// Why a task is blocked (SetTaskStatusWithReason). Cleared by any
		// later status change.
		version: 9,
		apply: func(tx *sql.Tx) error {
			return ensureColumn(tx, "tasks", "blocked_reason TEXT")
		},
	},
	{
		// Planning (plan.go): a plan is proposed as data and becomes tasks only
		// when a person approves it. tasks.plan_item_id records which item a
		// task came from.
		version: 10,
		apply: func(tx *sql.Tx) error {
			for _, ddl := range planTablesDDL {
				if _, err := tx.Exec(ddl); err != nil {
					return err
				}
			}
			return ensureColumn(tx, "tasks", "plan_item_id INTEGER")
		},
	}, {
		// spec_versions: the text a spec had before each revision (spec.go
		// ReviseSpec). New table only; it needs a migration so already-migrated
		// stores get it.
		version: 11,
		apply: func(tx *sql.Tx) error {
			_, err := tx.Exec(specVersionsDDL)
			return err
		},
	},
	{
		// Where a check came from and what it checked. source is "runner" when
		// acline ran the tool (`check run`) and "manual" when somebody recorded a
		// result; tree_hash fingerprints the working tree it was about (see
		// internal/worktree). Rows from before this migration are manual with no
		// tree, which is what they were. See CheckMeta.
		version: 12,
		apply: func(tx *sql.Tx) error {
			if err := ensureColumn(tx, "checks", "source TEXT NOT NULL DEFAULT 'manual'"); err != nil {
				return err
			}
			return ensureColumn(tx, "checks", "tree_hash TEXT")
		},
	},
	{
		// Events are looked up by type on every write (the hash_version marker,
		// the seal watermark) and by verify (row_seal); without an index each
		// of those scanned the whole audit trail. Every scoped list filters on
		// project_id, which had no index either.
		version: 13,
		apply: func(tx *sql.Tx) error {
			for _, ddl := range indexesV13 {
				if _, err := tx.Exec(ddl); err != nil {
					return err
				}
			}
			return nil
		},
	},
}

// indexesV13 are shared by `schema` (fresh stores) and migration 13.
var indexesV13 = []string{
	`CREATE INDEX IF NOT EXISTS idx_events_type ON events(type)`,
	`CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id)`,
	`CREATE INDEX IF NOT EXISTS idx_specs_project ON specs(project_id)`,
	`CREATE INDEX IF NOT EXISTS idx_decisions_project ON decisions(project_id)`,
	`CREATE INDEX IF NOT EXISTS idx_memory_project ON memory(project_id)`,
	`CREATE INDEX IF NOT EXISTS idx_milestones_project ON milestones(project_id)`,
	`CREATE INDEX IF NOT EXISTS idx_features_project ON features(project_id)`,
}

// ensureColumn adds colDef (e.g. "project_id INTEGER REFERENCES projects(id)")
// to table if a column with that name doesn't already exist. SQLite has no
// `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, so existence is checked via
// PRAGMA table_info first. No-op (including on a table that doesn't exist
// at all yet — nothing to migrate, `schema` will have created it fresh with
// the column already in place) rather than an error, so a migration can
// unconditionally list every table a column belongs to.
func ensureColumn(tx *sql.Tx, table, colDef string) error {
	colName := strings.Fields(colDef)[0]
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	exists := false
	var tableFound bool
	for rows.Next() {
		tableFound = true
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == colName {
			exists = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if !tableFound || exists {
		return nil
	}
	_, err = tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, colDef))
	return err
}

// runMigrations applies every migration newer than db's current
// PRAGMA user_version, each in its own transaction, bumping user_version as
// it goes so a later Open() on the same db skips what's already applied.
func runMigrations(db *sql.DB) error {
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		// Another process opening the same store may have applied it since
		// user_version was read above; the IMMEDIATE transaction makes this read
		// authoritative.
		var now int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&now); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: reading schema version: %w", m.version, err)
		}
		if now >= m.version {
			tx.Rollback()
			continue
		}
		if err := m.apply(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
		// PRAGMA user_version doesn't accept a bound parameter; m.version is
		// a fixed Go constant from the migrations slice above, never
		// user input, so inlining it here carries no injection risk.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: recording schema version: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
	}
	return nil
}

// Actor identifies who (or what) is writing a record. Resolved once per process
// from the environment so an agent-driven invocation self-attributes.
type Actor struct {
	Type  string // human | agent
	ID    string
	Model string
}

// ResolveActor reads ACLINE_ACTOR_TYPE, ACLINE_ACTOR and ACLINE_MODEL. When a model is
// set but no type is, the caller is assumed to be an agent.
func ResolveActor() Actor {
	a := Actor{
		Type:  os.Getenv("ACLINE_ACTOR_TYPE"),
		ID:    os.Getenv("ACLINE_ACTOR"),
		Model: os.Getenv("ACLINE_MODEL"),
	}
	if a.Type == "" {
		if a.Model != "" {
			a.Type = "agent"
		} else {
			a.Type = "human"
		}
	}
	if a.ID == "" {
		if a.Type == "agent" && a.Model != "" {
			a.ID = a.Model
		} else {
			a.ID = os.Getenv("USER")
		}
	}
	if a.ID == "" {
		a.ID = "unknown"
	}
	return a
}

type Store struct {
	DB    *sql.DB
	Actor Actor
	Path  string // resolved db file path, as passed to Open — used by guard.go to protect the actual db in use, not just the default filename

	// Embedder enables semantic search when set (see embedding.go and
	// internal/embed) — nil by default, so acline stays fully local/offline
	// unless the cmd layer explicitly wires one in (VOYAGE_API_KEY set).
	Embedder Embedder

	// EmbedBudget bounds how long a write waits for the embedding provider
	// (default 10s); see indexForSemanticSearch.
	EmbedBudget time.Duration

	// AuditErrorHandler, when set, receives secondary audit events that failed
	// to write (see auditFailed). Nil means "warn on stderr".
	AuditErrorHandler func(eventType string, err error)
}

// DefaultPath resolves the DB location: ACLINE_DB env var if set, otherwise a
// single global database shared across every tracked project — that's the
// whole point of the projects/project_id scoping (see project.go): one
// store, not one-DB-per-repo the way the pre-redesign tool worked. Default
// location follows the XDG base directory spec on the data-home var, falling
// back to ~/.acline/store.db. --db and ACLINE_DB remain the way to opt
// into a different (e.g. per-repo) location on purpose.
func DefaultPath() (string, error) {
	if p := os.Getenv("ACLINE_DB"); p != "" {
		return p, nil
	}
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		return filepath.Join(dataHome, "acline", "store.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".acline", "store.db"), nil
}

// dsnOptions are applied by the driver to every connection it opens (not just
// the first), so they survive connection recycling:
//   - busy_timeout: several processes share one store (a long-lived
//     `mcp serve`, per-tool-call hook invocations, interactive CLI). Without a
//     timeout any overlap failed instantly with SQLITE_BUSY.
//   - journal_mode=WAL: readers no longer block on a writer and vice versa.
//   - synchronous=NORMAL: the standard pairing with WAL.
//   - foreign_keys: previously a one-off Exec that only reached one connection.
//   - _txlock=immediate: db.Begin() takes the write lock up front, so two
//     read-then-write transactions (logEvent's prev_hash read) serialize
//     instead of one of them failing on lock upgrade.
const dsnOptions = "_busy_timeout=5000&_foreign_keys=1&_journal_mode=WAL&_txlock=immediate&_pragma=synchronous(NORMAL)"

func Open(path string) (*Store, error) {
	// The driver splits query options off at the first '?'.
	if strings.Contains(path, "?") {
		return nil, fmt.Errorf("db path %q must not contain '?'", path)
	}
	// The store holds specs, decisions, memory and an audit trail: create it
	// private to the user. Only what we create is restricted -- an existing
	// directory or database keeps whatever permissions its owner gave it. (SQLite
	// gives the -wal/-shm sidecars the main file's mode.)
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating db directory: %w", err)
		}
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("creating db file: %w", err)
		}
		f.Close()
	}
	db, err := sql.Open("sqlite", path+"?"+dsnOptions)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	db.SetMaxOpenConns(1) // modernc sqlite: keep in-process writes serialized
	if err := prepareSchema(db, path); err != nil {
		db.Close()
		return nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	return &Store{DB: db, Actor: ResolveActor(), Path: absPath}, nil
}

// prepareSchema brings db up to schemaVersion. The ~40-statement DDL and the
// migrations only run when PRAGMA user_version says the file is behind, so
// the common case (every hook/CLI invocation against an up-to-date store)
// costs one PRAGMA read. A store *newer* than this binary is refused rather
// than silently mutated by code that doesn't know its shape.
func prepareSchema(db *sql.DB, path string) error {
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than this acline build supports (%d) — upgrade acline", current, schemaVersion)
	}
	if current == schemaVersion {
		return nil
	}
	if current > 0 {
		if err := backupBeforeMigrating(db, path, current); err != nil {
			return fmt.Errorf("backing up the store before migrating it from schema %d to %d: %w", current, schemaVersion, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}
	if err := runMigrations(db); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	return nil
}

// backupBeforeMigrating copies the store to <path>.pre-v<from> before a migration
// changes it. A newer acline migrates the shared store the first time it opens
// it, and an older acline then refuses it, so without a copy the only way back
// is a backup somebody remembered to take. The copy is a consistent snapshot
// (VACUUM INTO), private to the user, and an existing one is never overwritten
// (it is the older, and so the more useful, state).
func backupBeforeMigrating(db *sql.DB, path string, from int) error {
	if path == "" || path == ":memory:" {
		return nil
	}
	backup := fmt.Sprintf("%s.pre-v%d", path, from)
	if _, err := os.Stat(backup); err == nil {
		return nil
	}
	if _, err := db.Exec(`VACUUM INTO ?`, backup); err != nil {
		// Another process opening the store at the same moment wrote it first;
		// that copy is of the same, unmigrated state.
		if _, statErr := os.Stat(backup); statErr == nil {
			return nil
		}
		return err
	}
	return os.Chmod(backup, 0o600)
}

func (s *Store) Close() error {
	return s.DB.Close()
}
