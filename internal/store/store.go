// Package store persists sessions, the prompt queue, the per-session event
// log, automations and secrets in one SQLite file.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

var ErrNotFound = errors.New("not found")

// Store wraps two pools over the same file: a single-connection writer, which
// serializes writes (and so makes event sequence assignment atomic), and a
// read pool that WAL mode lets run alongside it.
type Store struct {
	w, r *sql.DB
	key  []byte
}

func Open(path string, secretKey []byte) (*Store, error) { return OpenWith(path, secretKey, Options{}) }

// Options change how a database is opened.
type Options struct {
	// BeforeMigrate runs when migrations are pending, before any is applied,
	// with their names and a function that copies the database, as it is,
	// to a new file. An error stops the open with the database untouched.
	BeforeMigrate func(pending []string, backup func(path string) error) error
}

// OpenWith opens a database, applying any pending migrations first.
func OpenWith(path string, secretKey []byte, opts Options) (*Store, error) {
	if len(secretKey) != 32 {
		return nil, errors.New("store: secret key must be 32 bytes")
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := Migrate(w, migrations, "migrations", opts.BeforeMigrate); err != nil {
		w.Close()
		return nil, fmt.Errorf("store: %s: %w", path, err)
	}
	// WAL mode persists in the file, so it is set once, and only after
	// migrate has made sure the file is ours.
	if _, err := w.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn+"&_pragma=query_only(1)")
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	return &Store{w: w, r: r, key: secretKey}, nil
}

func (s *Store) Close() error { return errors.Join(s.r.Close(), s.w.Close()) }

// Migrations lists the migrations applied to the database, in the order
// they were applied.
func (s *Store) Migrations(ctx context.Context) ([]string, error) {
	return query(ctx, s.r, func(r scanner) (string, error) {
		var name string
		return name, r.Scan(&name)
	}, `SELECT name FROM schema_migrations ORDER BY applied_at, rowid`)
}

// Migrate applies the *.sql files in dir of files that db hasn't had, in
// name order, each in its own transaction, recording each by name in
// schema_migrations. Recording names rather than a count lets branches
// developed side by side each add migrations. PRAGMA user_version still
// counts them: databases from before names were recorded counted their
// migrations there, so their first user_version migrations are recorded as
// applied. A database with tables but neither is someone else's, and is
// refused untouched. before, if set, runs when migrations are pending.
func Migrate(db *sql.DB, files fs.FS, dir string, before func(pending []string, backup func(path string) error) error) error {
	names, err := fs.Glob(files, dir+"/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	var version, tables int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE tbl_name != 'schema_migrations'`).Scan(&tables); err != nil {
		return err
	}
	if version == 0 && tables > 0 {
		return errors.New("not a beethoven database; move it aside or use another data directory")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	applied := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	rows.Close()
	base := func(name string) string { return strings.TrimPrefix(name, dir+"/") }
	if len(applied) == 0 && version > 0 { // counted, not yet named
		if version > len(names) {
			return fmt.Errorf("the database has %d migrations, more than this version of Beethoven knows", version)
		}
		for _, name := range names[:version] {
			if _, err := db.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, 0)`, base(name)); err != nil {
				return err
			}
			applied[base(name)] = true
		}
	}
	var pending []string
	for _, name := range names {
		if !applied[base(name)] {
			pending = append(pending, name)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if before != nil {
		backup := func(path string) error {
			_, err := db.Exec(`VACUUM INTO ?`, path)
			return err
		}
		short := make([]string, len(pending))
		for i, name := range pending {
			short[i] = base(name)
		}
		if err := before(short, backup); err != nil {
			return err
		}
	}
	for _, name := range pending {
		body, _ := fs.ReadFile(files, name)
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", base(name), err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, base(name), ms(time.Now())); err != nil {
			tx.Rollback()
			return err
		}
		applied[base(name)] = true
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(applied))); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// NewID returns a short random lowercase identifier.
func NewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))[:12]
}

type Author struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// RepoRef is a repository in a workspace. Each is cloned to
// /workspace/<name>; the first in a list is the primary.
type RepoRef struct {
	Repo       string `json:"repo"`        // owner/name
	BaseBranch string `json:"base_branch"` // "" = the repository's default branch
	PRURL      string `json:"pr_url,omitempty"`
}

// Dir is the repository's checkout directory under /workspace.
func (r RepoRef) Dir() string {
	_, name, _ := strings.Cut(r.Repo, "/")
	return name
}

// RepoNames lists the repositories, primary first.
func RepoNames(refs []RepoRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Repo
	}
	return out
}

func encodeRepos(refs []RepoRef) string {
	if refs == nil {
		refs = []RepoRef{}
	}
	b, _ := json.Marshal(refs)
	return string(b)
}

func decodeRepos(s string) []RepoRef {
	refs := []RepoRef{}
	json.Unmarshal([]byte(s), &refs)
	return refs
}

// ---------------------------------------------------------------- sessions

type Session struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Repos         []RepoRef `json:"repos"`
	EnvironmentID string    `json:"environment_id,omitempty"` // provenance; the environment may since have changed or gone
	Branch        string    `json:"branch"`
	Model         string    `json:"model"`
	Effort        string    `json:"effort"`
	ParentID      string    `json:"parent_id,omitempty"`
	Depth         int       `json:"depth"`
	Source        string    `json:"source"`
	Key           string    `json:"key,omitempty"`
	AutomationID  string    `json:"automation_id,omitempty"`
	Author        Author    `json:"author"`
	AccountID     string    `json:"account_id,omitempty"` // the person it acts for; their GitHub opens its pull requests
	TeamID        string    `json:"team_id,omitempty"`    // the owning team; "" = the workspace
	Visibility    string    `json:"visibility"`           // workspace, team or private
	Status        string    `json:"status"`
	Sandbox       string    `json:"sandbox"`
	SandboxID     string    `json:"-"`
	AgentSession  string    `json:"-"`
	CostUSD       float64   `json:"cost_usd"`
	Archived      bool      `json:"archived"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Primary is the session's first repository, if any.
func (x Session) Primary() (RepoRef, bool) {
	if len(x.Repos) == 0 {
		return RepoRef{}, false
	}
	return x.Repos[0], true
}

const sessionCols = `id, title, repos, environment_id, branch, model, effort, COALESCE(parent_id,''), depth,
	source, COALESCE(key,''), COALESCE(automation_id,''), author_name, author_email, account_id, owner_team_id,
	visibility, status, sandbox, sandbox_id, agent_session, cost_usd, archived, created_at, updated_at`

func scanSession(row scanner) (Session, error) {
	var x Session
	var repos string
	var created, updated int64
	err := row.Scan(&x.ID, &x.Title, &repos, &x.EnvironmentID, &x.Branch, &x.Model, &x.Effort, &x.ParentID,
		&x.Depth, &x.Source, &x.Key, &x.AutomationID, &x.Author.Name, &x.Author.Email, &x.AccountID, &x.TeamID,
		&x.Visibility, &x.Status, &x.Sandbox,
		&x.SandboxID, &x.AgentSession, &x.CostUSD, &x.Archived, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	x.Repos = decodeRepos(repos)
	x.CreatedAt, x.UpdatedAt = fromMS(created), fromMS(updated)
	return x, err
}

func (s *Store) CreateSession(ctx context.Context, x *Session) error {
	now := time.Now()
	x.CreatedAt, x.UpdatedAt = now, now
	if x.Repos == nil {
		x.Repos = []RepoRef{}
	}
	if x.Visibility == "" {
		x.Visibility = VisibilityWorkspace
	}
	if !ValidVisibility(x.Visibility, x.TeamID) {
		return fmt.Errorf("%w: visibility must be workspace, private or, for a team's session, team", ErrInvalid)
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO sessions (id, title, repos, environment_id, branch, model,
		effort, parent_id, depth, source, key, automation_id, author_name, author_email, account_id, owner_team_id,
		visibility, status, sandbox, sandbox_id, agent_session, cost_usd, archived, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		x.ID, x.Title, encodeRepos(x.Repos), x.EnvironmentID, x.Branch, x.Model, x.Effort, null(x.ParentID),
		x.Depth, x.Source, null(x.Key), null(x.AutomationID), x.Author.Name, x.Author.Email, x.AccountID, x.TeamID,
		x.Visibility, x.Status, x.Sandbox, x.SandboxID, x.AgentSession, x.CostUSD, x.Archived, ms(now), ms(now))
	return err
}

// SaveSession writes the mutable columns of x.
func (s *Store) SaveSession(ctx context.Context, x *Session) error {
	x.UpdatedAt = time.Now()
	_, err := s.w.ExecContext(ctx, `UPDATE sessions SET title=?, repos=?, branch=?, model=?, effort=?, visibility=?,
		status=?, sandbox=?, sandbox_id=?, agent_session=?, cost_usd=?, archived=?, updated_at=? WHERE id=?`,
		x.Title, encodeRepos(x.Repos), x.Branch, x.Model, x.Effort, x.Visibility, x.Status, x.Sandbox, x.SandboxID,
		x.AgentSession, x.CostUSD, x.Archived, ms(x.UpdatedAt), x.ID)
	return err
}

func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	return scanSession(s.r.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id=?`, id))
}

func (s *Store) SessionByKey(ctx context.Context, key string) (Session, error) {
	return scanSession(s.r.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE key=?`, key))
}

type SessionFilter struct {
	ParentID string   // only children of this session
	Archived *bool    // nil = both
	Sandbox  []string // only sessions whose sandbox is in one of these states
	Viewer   *Viewer  // only sessions this viewer may read; nil = all
	Teams    []string // only sessions owned by these teams ("" = the workspace)
	Account  string   // only sessions this person started
	Query    string   // only sessions whose title contains this, ignoring case
	Limit    int
}

func (s *Store) Sessions(ctx context.Context, f SessionFilter) ([]Session, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions WHERE 1=1`
	var args []any
	if f.Viewer != nil {
		pred, pargs := f.Viewer.readable()
		q += ` AND ` + pred
		args = append(args, pargs...)
	}
	if f.Teams != nil {
		q += ` AND owner_team_id IN (` + placeholders(len(f.Teams)) + `)`
		for _, t := range f.Teams {
			args = append(args, t)
		}
	}
	if f.Account != "" {
		q += ` AND account_id=?`
		args = append(args, f.Account)
	}
	if f.Archived != nil {
		q += ` AND archived=?`
		args = append(args, *f.Archived)
	}
	if f.Query != "" {
		q += ` AND title LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscaper.Replace(f.Query)+"%")
	}
	if f.ParentID != "" {
		q += ` AND parent_id=?`
		args = append(args, f.ParentID)
	}
	if len(f.Sandbox) > 0 {
		q += ` AND sandbox IN (?` + strings.Repeat(",?", len(f.Sandbox)-1) + `)`
		for _, v := range f.Sandbox {
			args = append(args, v)
		}
	}
	if f.Limit <= 0 {
		f.Limit = 200
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, f.Limit)
	return query(ctx, s.r, scanSession, q, args...)
}

// likeEscaper escapes LIKE's wildcards, so a query matches literally.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ----------------------------------------------------------------- prompts

const (
	Queued    = "queued"
	Running   = "running"
	Done      = "done"
	Failed    = "failed"
	Cancelled = "cancelled"
)

type Prompt struct {
	ID           string     `json:"id"`
	SessionID    string     `json:"session_id"`
	Content      string     `json:"content"`
	Model        string     `json:"model,omitempty"`
	Effort       string     `json:"effort,omitempty"`
	Author       Author     `json:"author"`
	AccountID    string     `json:"account_id,omitempty"`
	Source       string     `json:"source"`
	AutomationID string     `json:"automation_id,omitempty"`
	DedupKey     string     `json:"-"`
	Status       string     `json:"status"`
	Error        string     `json:"error,omitempty"`
	Result       string     `json:"result,omitempty"`
	CostUSD      float64    `json:"cost_usd"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	// Files attached to the prompt. Prompt and Prompts fill them in,
	// without their data.
	Attachments []Attachment `json:"attachments,omitempty"`
}

const promptCols = `id, session_id, content, model, effort, author_name, author_email, account_id, source,
	COALESCE(automation_id,''), COALESCE(dedup_key,''), status, error, result, cost_usd, created_at, started_at,
	finished_at`

func scanPrompt(row scanner) (Prompt, error) {
	var p Prompt
	var created int64
	var started, finished sql.NullInt64
	err := row.Scan(&p.ID, &p.SessionID, &p.Content, &p.Model, &p.Effort, &p.Author.Name, &p.Author.Email, &p.AccountID, &p.Source,
		&p.AutomationID, &p.DedupKey, &p.Status, &p.Error, &p.Result, &p.CostUSD, &created, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CreatedAt = fromMS(created)
	p.StartedAt, p.FinishedAt = optMS(started), optMS(finished)
	return p, err
}

// CreatePrompt queues p with its attachments. If p.DedupKey matches an
// existing prompt, p is replaced by that prompt and created is false.
func (s *Store) CreatePrompt(ctx context.Context, p *Prompt) (created bool, err error) {
	p.Status, p.CreatedAt = Queued, time.Now()
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO prompts (id, session_id, content, model, effort, author_name,
		author_email, account_id, source, automation_id, dedup_key, status, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(dedup_key) DO NOTHING`,
		p.ID, p.SessionID, p.Content, p.Model, p.Effort, p.Author.Name, p.Author.Email, p.AccountID, p.Source,
		null(p.AutomationID), null(p.DedupKey), p.Status, ms(p.CreatedAt))
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if *p, err = scanPrompt(tx.QueryRowContext(ctx, `SELECT `+promptCols+` FROM prompts WHERE dedup_key=?`,
			p.DedupKey)); err != nil {
			return false, err
		}
		files, err := promptAttachments(ctx, tx, `prompt_id=?`, p.ID)
		p.Attachments = files[p.ID]
		return false, err
	}
	if err := addAttachments(ctx, tx, p); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) Prompt(ctx context.Context, id string) (Prompt, error) {
	p, err := scanPrompt(s.r.QueryRowContext(ctx, `SELECT `+promptCols+` FROM prompts WHERE id=?`, id))
	if err != nil {
		return p, err
	}
	files, err := promptAttachments(ctx, s.r, `prompt_id=?`, id)
	p.Attachments = files[id]
	return p, err
}

func (s *Store) PromptByDedupKey(ctx context.Context, key string) (Prompt, error) {
	return scanPrompt(s.r.QueryRowContext(ctx, `SELECT `+promptCols+` FROM prompts WHERE dedup_key=?`, key))
}

func (s *Store) Prompts(ctx context.Context, sessionID string) ([]Prompt, error) {
	ps, err := query(ctx, s.r, scanPrompt, `SELECT `+promptCols+` FROM prompts WHERE session_id=?
		ORDER BY created_at, rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	files, err := promptAttachments(ctx, s.r, `session_id=?`, sessionID)
	for i := range ps {
		ps[i].Attachments = files[ps[i].ID]
	}
	return ps, err
}

// ClaimNext marks the oldest queued prompt of a session running and returns it.
func (s *Store) ClaimNext(ctx context.Context, sessionID string) (Prompt, error) {
	return scanPrompt(s.w.QueryRowContext(ctx, `UPDATE prompts SET status='running', started_at=?
		WHERE id = (SELECT id FROM prompts WHERE session_id=? AND status='queued' ORDER BY created_at, rowid LIMIT 1)
		RETURNING `+promptCols, ms(time.Now()), sessionID))
}

func (s *Store) FinishPrompt(ctx context.Context, p *Prompt) error {
	now := time.Now()
	p.FinishedAt = &now
	_, err := s.w.ExecContext(ctx, `UPDATE prompts SET status=?, error=?, result=?, cost_usd=?, finished_at=?
		WHERE id=?`, p.Status, p.Error, p.Result, p.CostUSD, ms(now), p.ID)
	return err
}

// CancelQueued cancels one queued prompt, or every queued prompt of the
// session when promptID is empty, and returns what it cancelled.
func (s *Store) CancelQueued(ctx context.Context, sessionID, promptID string) ([]Prompt, error) {
	return query(ctx, s.w, scanPrompt, `UPDATE prompts SET status='cancelled', finished_at=?
		WHERE session_id=? AND status='queued' AND (?='' OR id=?) RETURNING `+promptCols,
		ms(time.Now()), sessionID, promptID, promptID)
}

// RecoverRunning fails prompts left running by a previous process.
func (s *Store) RecoverRunning(ctx context.Context, reason string) ([]Prompt, error) {
	return query(ctx, s.w, scanPrompt, `UPDATE prompts SET status='failed', error=?, finished_at=?
		WHERE status='running' RETURNING `+promptCols, reason, ms(time.Now()))
}

// QueuedSessions lists sessions with prompts waiting to run.
func (s *Store) QueuedSessions(ctx context.Context) ([]string, error) {
	return query(ctx, s.r, func(r scanner) (id string, err error) { return id, r.Scan(&id) }, `SELECT DISTINCT session_id FROM prompts WHERE status='queued'`)
}

func (s *Store) AutomationRuns(ctx context.Context, automationID string, limit int) ([]Prompt, error) {
	return query(ctx, s.r, scanPrompt, `SELECT `+promptCols+` FROM prompts WHERE automation_id=?
		ORDER BY created_at DESC LIMIT ?`, automationID, limit)
}

func (s *Store) AutomationBusy(ctx context.Context, automationID string) (bool, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompts WHERE automation_id=? AND status IN ('queued','running')`,
		automationID).Scan(&n)
	return n > 0, err
}

// ------------------------------------------------------------------ events

type Event struct {
	Session string          `json:"session"`
	Seq     int64           `json:"seq,omitempty"` // 0 = ephemeral, never stored
	Type    string          `json:"type"`
	Prompt  string          `json:"prompt,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	At      time.Time       `json:"at"`
}

// AppendEvent stores e and assigns its sequence number.
func (s *Store) AppendEvent(ctx context.Context, e *Event) error {
	return s.w.QueryRowContext(ctx, `INSERT INTO events (session_id, seq, type, prompt_id, data, at)
		VALUES (?, (SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE session_id=?), ?, ?, ?, ?) RETURNING seq`,
		e.Session, e.Session, e.Type, e.Prompt, string(e.Data), ms(e.At)).Scan(&e.Seq)
}

func (s *Store) Events(ctx context.Context, sessionID string, after int64, limit int) ([]Event, error) {
	return query(ctx, s.r, func(r scanner) (e Event, err error) {
		var data string
		var at int64
		err = r.Scan(&e.Session, &e.Seq, &e.Type, &e.Prompt, &data, &at)
		e.Data, e.At = json.RawMessage(data), fromMS(at)
		return e, err
	}, `SELECT session_id, seq, type, prompt_id, data, at FROM events
		WHERE session_id=? AND seq>? ORDER BY seq LIMIT ?`, sessionID, after, limit)
}

// EventsOfType returns a session's stored events of the given types, oldest
// first.
func (s *Store) EventsOfType(ctx context.Context, sessionID string, types ...string) ([]Event, error) {
	if len(types) == 0 {
		return nil, nil
	}
	args := []any{sessionID}
	for _, t := range types {
		args = append(args, t)
	}
	return query(ctx, s.r, func(r scanner) (e Event, err error) {
		var data string
		var at int64
		err = r.Scan(&e.Session, &e.Seq, &e.Type, &e.Prompt, &data, &at)
		e.Data, e.At = json.RawMessage(data), fromMS(at)
		return e, err
	}, `SELECT session_id, seq, type, prompt_id, data, at FROM events
		WHERE session_id=? AND type IN (?`+strings.Repeat(",?", len(types)-1)+`) ORDER BY seq`, args...)
}

// ------------------------------------------------------------- automations

type Automation struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Repos         []RepoRef  `json:"repos"`
	EnvironmentID string     `json:"environment_id,omitempty"`
	Prompt        string     `json:"prompt"`
	Model         string     `json:"model"`
	Effort        string     `json:"effort"`
	Schedule      string     `json:"schedule"`
	TeamID        string     `json:"team_id,omitempty"`    // the owning team; "" = the workspace
	AccountID     string     `json:"account_id,omitempty"` // who it runs as: whoever made it
	Reuse         bool       `json:"reuse"`
	Enabled       bool       `json:"enabled"`
	Failures      int        `json:"failures"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	HookHash      string     `json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
}

const automationCols = `id, name, repos, environment_id, prompt, model, effort, schedule, reuse, enabled,
	failures, next_run_at, hook_hash, created_at, owner_team_id, account_id`

func scanAutomation(row scanner) (Automation, error) {
	var a Automation
	var repos string
	var next sql.NullInt64
	var created int64
	err := row.Scan(&a.ID, &a.Name, &repos, &a.EnvironmentID, &a.Prompt, &a.Model, &a.Effort, &a.Schedule,
		&a.Reuse, &a.Enabled, &a.Failures, &next, &a.HookHash, &created, &a.TeamID, &a.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Repos = decodeRepos(repos)
	a.NextRunAt, a.CreatedAt = optMS(next), fromMS(created)
	return a, err
}

func (s *Store) CreateAutomation(ctx context.Context, a *Automation) error {
	a.CreatedAt = time.Now()
	_, err := s.w.ExecContext(ctx, `INSERT INTO automations (`+automationCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, encodeRepos(a.Repos), a.EnvironmentID, a.Prompt, a.Model, a.Effort, a.Schedule, a.Reuse,
		a.Enabled, a.Failures, optToMS(a.NextRunAt), a.HookHash, ms(a.CreatedAt), a.TeamID, a.AccountID)
	return err
}

func (s *Store) SaveAutomation(ctx context.Context, a *Automation) error {
	_, err := s.w.ExecContext(ctx, `UPDATE automations SET name=?, repos=?, environment_id=?, prompt=?, model=?,
		effort=?, schedule=?, reuse=?, enabled=?, failures=?, next_run_at=? WHERE id=?`,
		a.Name, encodeRepos(a.Repos), a.EnvironmentID, a.Prompt, a.Model, a.Effort, a.Schedule, a.Reuse,
		a.Enabled, a.Failures, optToMS(a.NextRunAt), a.ID)
	return err
}

func (s *Store) Automation(ctx context.Context, id string) (Automation, error) {
	return scanAutomation(s.r.QueryRowContext(ctx, `SELECT `+automationCols+` FROM automations WHERE id=?`, id))
}

func (s *Store) Automations(ctx context.Context) ([]Automation, error) {
	return query(ctx, s.r, scanAutomation, `SELECT `+automationCols+` FROM automations ORDER BY created_at DESC`)
}

func (s *Store) DeleteAutomation(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM automations WHERE id=?`, id)
	return err
}

func (s *Store) DueAutomations(ctx context.Context, now time.Time) ([]Automation, error) {
	return query(ctx, s.r, scanAutomation, `SELECT `+automationCols+` FROM automations
		WHERE enabled=1 AND schedule!='' AND next_run_at<=?`, ms(now))
}

// AdvanceSchedule moves next_run_at from prev to next. It reports false if
// another tick already advanced it, so each due time fires once.
func (s *Store) AdvanceSchedule(ctx context.Context, id string, prev, next time.Time) (bool, error) {
	res, err := s.w.ExecContext(ctx, `UPDATE automations SET next_run_at=? WHERE id=? AND next_run_at=?`,
		ms(next), id, ms(prev))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ------------------------------------------------------------------ helpers

type scanner interface{ Scan(...any) error }

// queryer is a database or a transaction.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// query runs q on db and scans every row with scan.
func query[T any](ctx context.Context, db queryer, scan func(scanner) (T, error), q string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// placeholders is n comma-separated SQL parameters.
func placeholders(n int) string {
	if n == 0 {
		return "NULL" // IN (NULL) matches nothing
	}
	return "?" + strings.Repeat(",?", n-1)
}

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ms(t time.Time) int64     { return t.UnixMilli() }
func fromMS(v int64) time.Time { return time.UnixMilli(v) }
func optToMS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ms(*t)
}
func optMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}
