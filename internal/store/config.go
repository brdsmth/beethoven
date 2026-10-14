package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("already exists")
)

// ---------------------------------------------------------------- settings

// Overrides are the settings an environment can change. Zero values inherit.
type Overrides struct {
	Model        string  `json:"model,omitempty"`
	Effort       string  `json:"effort,omitempty"`       // low, medium, high, xhigh, max
	Instructions string  `json:"instructions,omitempty"` // appended to the agent's system prompt
	Image        string  `json:"image,omitempty"`        // sandbox image, used when a sandbox is created
	CPUs         float64 `json:"cpus,omitempty"`
	MemoryMB     int     `json:"memory_mb,omitempty"`
}

// Over returns o with unset fields taken from base. Instructions accumulate,
// base first, so global instructions still apply inside an environment.
func (o Overrides) Over(base Overrides) Overrides {
	pick := func(v, def string) string {
		if v != "" {
			return v
		}
		return def
	}
	out := Overrides{
		Model: pick(o.Model, base.Model), Effort: pick(o.Effort, base.Effort), Image: pick(o.Image, base.Image),
		CPUs: o.CPUs, MemoryMB: o.MemoryMB,
	}
	if out.CPUs == 0 {
		out.CPUs = base.CPUs
	}
	if out.MemoryMB == 0 {
		out.MemoryMB = base.MemoryMB
	}
	out.Instructions = strings.TrimSpace(base.Instructions + "\n\n" + o.Instructions)
	return out
}

// Settings are the global defaults; zero values fall back to the server's.
type Settings struct {
	Overrides
	IdleMinutes  int    `json:"idle_minutes,omitempty"` // stop idle sandboxes after
	TurnMinutes  int    `json:"turn_minutes,omitempty"` // limit for one prompt
	DraftPRs     bool   `json:"draft_prs,omitempty"`    // open pull requests as drafts by default
	BranchPrefix string `json:"branch_prefix,omitempty"`
	AuthorName   string `json:"author_name,omitempty"` // commit author for prompts from the UI and CLI
	AuthorEmail  string `json:"author_email,omitempty"`
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var v string
	var out Settings
	err := s.r.QueryRowContext(ctx, `SELECT value FROM settings WHERE id=1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal([]byte(v), &out)
}

func (s *Store) SaveSettings(ctx context.Context, v Settings) error {
	b, _ := json.Marshal(v)
	_, err := s.w.ExecContext(ctx, `INSERT INTO settings (id, value) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET value=excluded.value`, string(b))
	return err
}

// ------------------------------------------------------------ environments

// Environment is a reusable workspace: repositories, scripts, secrets and
// setting overrides.
type Environment struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Repos          []RepoRef `json:"repos"`
	SetupScript    string    `json:"setup_script"`
	StartScript    string    `json:"start_script"`
	InheritSecrets bool      `json:"inherit_secrets"`
	Overrides      Overrides `json:"overrides"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const envCols = `id, name, description, repos, setup_script, start_script, inherit_secrets, overrides, created_at, updated_at`

func scanEnvironment(row scanner) (Environment, error) {
	var e Environment
	var repos, overrides string
	var created, updated int64
	err := row.Scan(&e.ID, &e.Name, &e.Description, &repos, &e.SetupScript, &e.StartScript, &e.InheritSecrets,
		&overrides, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	e.Repos = decodeRepos(repos)
	json.Unmarshal([]byte(overrides), &e.Overrides)
	e.CreatedAt, e.UpdatedAt = fromMS(created), fromMS(updated)
	return e, err
}

func (s *Store) CreateEnvironment(ctx context.Context, e *Environment) error {
	e.CreatedAt, e.UpdatedAt = time.Now(), time.Now()
	ov, _ := json.Marshal(e.Overrides)
	_, err := s.w.ExecContext(ctx, `INSERT INTO environments (`+envCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Name, e.Description, encodeRepos(e.Repos), e.SetupScript, e.StartScript, e.InheritSecrets,
		string(ov), ms(e.CreatedAt), ms(e.UpdatedAt))
	return conflict(err)
}

func (s *Store) SaveEnvironment(ctx context.Context, e *Environment) error {
	e.UpdatedAt = time.Now()
	ov, _ := json.Marshal(e.Overrides)
	_, err := s.w.ExecContext(ctx, `UPDATE environments SET name=?, description=?, repos=?, setup_script=?,
		start_script=?, inherit_secrets=?, overrides=?, updated_at=? WHERE id=?`,
		e.Name, e.Description, encodeRepos(e.Repos), e.SetupScript, e.StartScript, e.InheritSecrets, string(ov),
		ms(e.UpdatedAt), e.ID)
	return conflict(err)
}

func (s *Store) Environment(ctx context.Context, id string) (Environment, error) {
	return scanEnvironment(s.r.QueryRowContext(ctx, `SELECT `+envCols+` FROM environments WHERE id=?`, id))
}

func (s *Store) Environments(ctx context.Context) ([]Environment, error) {
	return query(ctx, s.r, scanEnvironment, `SELECT `+envCols+` FROM environments ORDER BY name`)
}

// DeleteEnvironment removes an environment with its secrets and MCP servers.
// Sessions started from it keep their copy of its repositories.
func (s *Store) DeleteEnvironment(ctx context.Context, id string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM secrets WHERE scope=?`, `DELETE FROM mcp_servers WHERE scope=?`} {
		if _, err := tx.ExecContext(ctx, q, EnvScope(id)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM environments WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ------------------------------------------------------------- MCP servers

// MCPServer is an extra tool server offered to agents in its scope.
type MCPServer struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Scope      string            `json:"scope"` // "", EnvScope(id) or owner/name
	Type       string            `json:"type"`  // http | stdio
	URL        string            `json:"url,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Secret     map[string]string `json:"-"`           // headers (http) or environment (stdio); write-only
	SecretKeys []string          `json:"secret_keys"` // the names in Secret, for display
	Enabled    bool              `json:"enabled"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

const mcpCols = `id, name, scope, type, url, command, secret, enabled, created_at, updated_at`

func (s *Store) scanMCP(row scanner) (MCPServer, error) {
	var m MCPServer
	var command string
	var secret []byte
	var created, updated int64
	err := row.Scan(&m.ID, &m.Name, &m.Scope, &m.Type, &m.URL, &command, &secret, &m.Enabled, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	json.Unmarshal([]byte(command), &m.Command)
	m.Secret = map[string]string{}
	if len(secret) > 0 {
		plain, err := s.open(secret)
		if err != nil {
			return m, err
		}
		json.Unmarshal(plain, &m.Secret)
	}
	m.SecretKeys = sortedKeys(m.Secret)
	m.CreatedAt, m.UpdatedAt = fromMS(created), fromMS(updated)
	return m, nil
}

func (s *Store) mcpArgs(m *MCPServer) (command string, secret []byte, err error) {
	cmd, _ := json.Marshal(m.Command)
	if m.Command == nil {
		cmd = []byte("[]")
	}
	if len(m.Secret) > 0 {
		plain, _ := json.Marshal(m.Secret)
		if secret, err = s.seal(plain); err != nil {
			return "", nil, err
		}
	}
	m.SecretKeys = sortedKeys(m.Secret)
	return string(cmd), secret, nil
}

func (s *Store) CreateMCPServer(ctx context.Context, m *MCPServer) error {
	m.CreatedAt, m.UpdatedAt = time.Now(), time.Now()
	cmd, secret, err := s.mcpArgs(m)
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO mcp_servers (`+mcpCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Name, m.Scope, m.Type, m.URL, cmd, secret, m.Enabled, ms(m.CreatedAt), ms(m.UpdatedAt))
	return conflict(err)
}

func (s *Store) SaveMCPServer(ctx context.Context, m *MCPServer) error {
	m.UpdatedAt = time.Now()
	cmd, secret, err := s.mcpArgs(m)
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `UPDATE mcp_servers SET name=?, scope=?, type=?, url=?, command=?, secret=?,
		enabled=?, updated_at=? WHERE id=?`, m.Name, m.Scope, m.Type, m.URL, cmd, secret, m.Enabled,
		ms(m.UpdatedAt), m.ID)
	return conflict(err)
}

func (s *Store) MCPServer(ctx context.Context, id string) (MCPServer, error) {
	return s.scanMCP(s.r.QueryRowContext(ctx, `SELECT `+mcpCols+` FROM mcp_servers WHERE id=?`, id))
}

func (s *Store) MCPServers(ctx context.Context) ([]MCPServer, error) {
	return query(ctx, s.r, s.scanMCP, `SELECT `+mcpCols+` FROM mcp_servers ORDER BY name`)
}

// MCPServersFor returns the enabled servers that apply to a session in
// environmentID (may be empty) working on repos.
func (s *Store) MCPServersFor(ctx context.Context, environmentID string, repos []string) ([]MCPServer, error) {
	all, err := s.MCPServers(ctx)
	if err != nil {
		return nil, err
	}
	var out []MCPServer
	for _, m := range all {
		if m.Enabled && (m.Scope == "" || (environmentID != "" && m.Scope == EnvScope(environmentID)) || contains(repos, m.Scope)) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Store) DeleteMCPServer(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id=?`, id)
	return err
}

// ------------------------------------------------------------------ helpers

// conflict maps unique-constraint violations to ErrConflict.
func conflict(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
