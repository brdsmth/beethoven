package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Session visibility: who may read a session, beyond the person who
// started it and the people it is shared with.
const (
	VisibilityWorkspace = "workspace" // everyone
	VisibilityTeam      = "team"      // the owning team's members, and admins unless the team is personal
	VisibilityPrivate   = "private"   // the person who started it, and owners unless the team is personal
)

// ValidVisibility reports whether v suits a session owned by teamID.
func ValidVisibility(v, teamID string) bool {
	return v == VisibilityWorkspace || v == VisibilityPrivate || (v == VisibilityTeam && teamID != "")
}

// Team roles and join policies.
const (
	TeamLead       = "lead"
	TeamMemberRole = "member"
	JoinOpen       = "open"   // anyone may join
	JoinInvite     = "invite" // leads add people
)

// TeamScope names a team's secrets and MCP servers.
func TeamScope(teamID string) string { return "team:" + teamID }

// IsTeamScope reports whether scope is a team's.
func IsTeamScope(scope string) bool { return strings.HasPrefix(scope, "team:") }

// TeamOfScope is the team a team scope names.
func TeamOfScope(scope string) string { return strings.TrimPrefix(scope, "team:") }

// A personal team is one person's own: it holds their work until they share
// it, has them alone as its lead, and nobody else sees it, admins included.
// Its id, and short name, are derived from the account's.
const personalPrefix = "personal-"

// PersonalTeamID is the id of an account's personal team.
func PersonalTeamID(accountID string) string { return personalPrefix + accountID }

// IsPersonalTeam reports whether a team id names a personal team.
func IsPersonalTeam(teamID string) bool { return strings.HasPrefix(teamID, personalPrefix) }

// PersonalOwner is the account whose personal team teamID is, if it is one.
func PersonalOwner(teamID string) string {
	if !IsPersonalTeam(teamID) {
		return ""
	}
	return strings.TrimPrefix(teamID, personalPrefix)
}

// Team groups people and owns work.
type Team struct {
	ID                   string     `json:"id"`
	Slug                 string     `json:"slug"`
	Name                 string     `json:"name"`
	Description          string     `json:"description"`
	Color                string     `json:"color"`
	JoinPolicy           string     `json:"join_policy"`            // open or invite
	DefaultVisibility    string     `json:"default_visibility"`     // team or workspace
	DefaultEnvironmentID string     `json:"default_environment_id"` // the project new sessions start in
	ArchivedAt           *time.Time `json:"archived_at,omitempty"`
	Personal             bool       `json:"personal,omitempty"` // one person's own team
	Members              int        `json:"members"`            // how many, when listed
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

var (
	slugPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
	colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Slugify makes a URL-safe slug from a name.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// Validate checks a team's editable fields, filling in defaults.
func (t *Team) Validate() error {
	t.Name, t.Description = strings.TrimSpace(t.Name), strings.TrimSpace(t.Description)
	if t.Slug == "" {
		t.Slug = Slugify(t.Name)
	}
	t.Slug = strings.ToLower(strings.TrimSpace(t.Slug))
	if t.JoinPolicy == "" {
		t.JoinPolicy = JoinInvite
	}
	if t.DefaultVisibility == "" {
		t.DefaultVisibility = VisibilityTeam
	}
	switch {
	case t.Name == "" || len(t.Name) > 60:
		return fmt.Errorf("%w: a team needs a name of at most 60 characters", ErrInvalid)
	case len(t.Description) > 300:
		return fmt.Errorf("%w: the description is longer than 300 characters", ErrInvalid)
	case !slugPattern.MatchString(t.Slug):
		return fmt.Errorf("%w: a team's short name uses lowercase letters, digits and dashes", ErrInvalid)
	case IsPersonalTeam(t.ID) && t.Slug != t.ID:
		return fmt.Errorf("%w: a personal team keeps its short name", ErrInvalid)
	case !IsPersonalTeam(t.ID) && strings.HasPrefix(t.Slug, personalPrefix):
		return fmt.Errorf("%w: short names starting with %q are for personal teams", ErrInvalid, personalPrefix)
	case IsPersonalTeam(t.ID) && t.JoinPolicy != JoinInvite:
		return fmt.Errorf("%w: nobody else can join a personal team", ErrInvalid)
	case t.Color != "" && !colorPattern.MatchString(t.Color):
		return fmt.Errorf("%w: color must look like #3366ff", ErrInvalid)
	case t.JoinPolicy != JoinOpen && t.JoinPolicy != JoinInvite:
		return fmt.Errorf("%w: join policy must be open or invite", ErrInvalid)
	case t.DefaultVisibility != VisibilityTeam && t.DefaultVisibility != VisibilityWorkspace:
		return fmt.Errorf("%w: default visibility must be team or workspace", ErrInvalid)
	}
	return nil
}

const teamCols = `t.id, t.slug, t.name, t.description, t.color, t.join_policy, t.default_visibility,
	t.default_environment_id, t.archived_at, (SELECT COUNT(*) FROM team_members m WHERE m.team_id = t.id),
	t.created_at, t.updated_at`

func scanTeam(row scanner) (Team, error) {
	var t Team
	var archived sql.NullInt64
	var created, updated int64
	err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.Description, &t.Color, &t.JoinPolicy, &t.DefaultVisibility,
		&t.DefaultEnvironmentID, &archived, &t.Members, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	t.ArchivedAt, t.CreatedAt, t.UpdatedAt = optMS(archived), fromMS(created), fromMS(updated)
	t.Personal = IsPersonalTeam(t.ID)
	return t, err
}

// CreateTeam stores a new team; leadID, if set, becomes its first lead.
func (s *Store) CreateTeam(ctx context.Context, t *Team, leadID string) error {
	if err := t.Validate(); err != nil {
		return err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t.ID, t.CreatedAt, t.UpdatedAt = NewID(), time.Now(), time.Now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO teams (id, slug, name, description, color, join_policy,
		default_visibility, default_environment_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Slug, t.Name, t.Description, t.Color, t.JoinPolicy, t.DefaultVisibility, t.DefaultEnvironmentID,
		ms(t.CreatedAt), ms(t.UpdatedAt)); err != nil {
		if conflict(err) == ErrConflict {
			return fmt.Errorf("%w: a team called %q", ErrConflict, t.Slug)
		}
		return err
	}
	if leadID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, account_id, role, created_at) VALUES (?,?,?,?)`,
			t.ID, leadID, TeamLead, ms(t.CreatedAt)); err != nil {
			return err
		}
		t.Members = 1
	}
	return tx.Commit()
}

// createPersonalTeam gives an account its personal team, within tx, and
// makes it where the account's work goes.
func createPersonalTeam(ctx context.Context, tx *sql.Tx, accountID string, at time.Time) error {
	id := PersonalTeamID(accountID)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO teams (id, slug, name, description, join_policy,
		default_visibility, created_at, updated_at) VALUES (?,?,'Personal','Only you see what''s here.','invite','team',?,?)`,
		id, id, ms(at), ms(at)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO team_members (team_id, account_id, role, created_at)
		VALUES (?,?,'lead',?)`, id, accountID, ms(at)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE accounts SET default_team_id=? WHERE id=? AND default_team_id=''`, id, accountID)
	return err
}

// SaveTeam writes a team's editable fields.
func (s *Store) SaveTeam(ctx context.Context, t *Team) error {
	if err := t.Validate(); err != nil {
		return err
	}
	t.UpdatedAt = time.Now()
	_, err := s.w.ExecContext(ctx, `UPDATE teams SET slug=?, name=?, description=?, color=?, join_policy=?,
		default_visibility=?, default_environment_id=?, updated_at=? WHERE id=?`, t.Slug, t.Name, t.Description,
		t.Color, t.JoinPolicy, t.DefaultVisibility, t.DefaultEnvironmentID, ms(t.UpdatedAt), t.ID)
	if conflict(err) == ErrConflict {
		return fmt.Errorf("%w: a team called %q", ErrConflict, t.Slug)
	}
	return err
}

// ArchiveTeam archives or restores a team. An archived team keeps its
// members and history but takes no new work.
func (s *Store) ArchiveTeam(ctx context.Context, id string, archived bool) error {
	var at any
	if archived {
		at = ms(time.Now())
	}
	res, err := s.w.ExecContext(ctx, `UPDATE teams SET archived_at=CASE WHEN ? THEN COALESCE(archived_at, ?) ELSE NULL END,
		updated_at=? WHERE id=?`, archived, at, ms(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Team(ctx context.Context, id string) (Team, error) {
	return scanTeam(s.r.QueryRowContext(ctx, `SELECT `+teamCols+` FROM teams t WHERE t.id=?`, id))
}

// TeamBySlug finds a team by its short name, or its id.
func (s *Store) TeamBySlug(ctx context.Context, slug string) (Team, error) {
	return scanTeam(s.r.QueryRowContext(ctx, `SELECT `+teamCols+` FROM teams t WHERE t.slug=? OR t.id=?`, slug, slug))
}

// Teams lists every team, active ones first, by name.
func (s *Store) Teams(ctx context.Context) ([]Team, error) {
	return query(ctx, s.r, scanTeam, `SELECT `+teamCols+` FROM teams t ORDER BY t.archived_at IS NOT NULL, lower(t.name)`)
}

// TeamMember is a person in a team.
type TeamMember struct {
	Account
	TeamRole string    `json:"team_role"` // lead or member
	JoinedAt time.Time `json:"joined_at"`
}

// TeamMembers lists a team's people, leads first.
func (s *Store) TeamMembers(ctx context.Context, teamID string) ([]TeamMember, error) {
	return query(ctx, s.r, func(r scanner) (TeamMember, error) {
		var m TeamMember
		var joined int64
		a, err := scanAccount(r, &m.TeamRole, &joined)
		m.Account, m.JoinedAt = a, fromMS(joined)
		return m, err
	}, `SELECT `+accountCols+`, m.role, m.created_at FROM team_members m JOIN accounts a ON a.id = m.account_id
		WHERE m.team_id=? ORDER BY m.role != 'lead', lower(COALESCE(NULLIF(a.name,''), a.email))`, teamID)
}

// ErrPersonalTeam refuses a change to who is in a personal team.
var ErrPersonalTeam = fmt.Errorf("%w: a personal team is its person's alone; share work through another team", ErrInvalid)

// ErrLastLead refuses a change that would leave a team without a lead.
var ErrLastLead = fmt.Errorf("%w: a team needs at least one lead; make someone else a lead first", ErrInvalid)

// SetTeamMember adds someone to a team, or changes their role in it.
func (s *Store) SetTeamMember(ctx context.Context, teamID, accountID, role string) error {
	if role != TeamLead && role != TeamMemberRole {
		return fmt.Errorf("%w: a team role is lead or member", ErrInvalid)
	}
	if IsPersonalTeam(teamID) {
		return ErrPersonalTeam
	}
	return s.changeMembership(ctx, teamID, accountID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, account_id, role, created_at) VALUES (?,?,?,?)
			ON CONFLICT(team_id, account_id) DO UPDATE SET role=excluded.role`, teamID, accountID, role, ms(time.Now()))
		return err
	}, role != TeamLead)
}

// RemoveTeamMember takes someone out of a team.
func (s *Store) RemoveTeamMember(ctx context.Context, teamID, accountID string) error {
	if IsPersonalTeam(teamID) {
		return ErrPersonalTeam
	}
	return s.changeMembership(ctx, teamID, accountID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id=? AND account_id=?`, teamID, accountID)
		return err
	}, true)
}

// changeMembership applies change, refusing to remove a team's last lead
// when demotes is set.
func (s *Store) changeMembership(ctx context.Context, teamID, accountID string, change func(*sql.Tx) error, demotes bool) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if demotes {
		var isLead bool
		var others int
		if err := tx.QueryRowContext(ctx, `SELECT
			EXISTS (SELECT 1 FROM team_members WHERE team_id=? AND account_id=? AND role='lead'),
			(SELECT COUNT(*) FROM team_members WHERE team_id=? AND account_id != ? AND role='lead')`,
			teamID, accountID, teamID, accountID).Scan(&isLead, &others); err != nil {
			return err
		}
		if isLead && others == 0 {
			return ErrLastLead
		}
	}
	if err := change(tx); err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return ErrNotFound
		}
		return err
	}
	return tx.Commit()
}

// Memberships maps the teams a person belongs to onto their role in each.
func (s *Store) Memberships(ctx context.Context, accountID string) (map[string]string, error) {
	out := map[string]string{}
	_, err := query(ctx, s.r, func(r scanner) (struct{}, error) {
		var team, role string
		err := r.Scan(&team, &role)
		out[team] = role
		return struct{}{}, err
	}, `SELECT team_id, role FROM team_members WHERE account_id=?`, accountID)
	return out, err
}

// DefaultTeam is where a person's work goes when nothing else says: the
// team they chose, if they're still in it, else their personal team.
func (s *Store) DefaultTeam(ctx context.Context, accountID string) (string, error) {
	var id string
	err := s.r.QueryRowContext(ctx, `SELECT COALESCE((SELECT a.default_team_id FROM accounts a JOIN team_members m
		ON m.team_id = a.default_team_id AND m.account_id = a.id WHERE a.id=?),
		(SELECT team_id FROM team_members WHERE team_id=? AND account_id=?), '')`,
		accountID, PersonalTeamID(accountID), accountID).Scan(&id)
	return id, err
}

// SetDefaultTeam sets where a person's work goes when nothing else says.
func (s *Store) SetDefaultTeam(ctx context.Context, accountID, teamID string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE accounts SET default_team_id=? WHERE id=?`, teamID, accountID)
	return err
}

// ----------------------------------------------------------------- viewers

// Viewer is who is reading, for the list queries that must show only what
// they may see. It mirrors access.ReadSession; keep the two in step.
type Viewer struct {
	AccountID string
	Server    bool // the server itself, which sees everything
	Admin     bool // workspace owners and admins
	Owner     bool // workspace owners
	Teams     []string
}

// readable selects the sessions the viewer may read.
func (v Viewer) readable() (string, []any) {
	args := []any{v.Server, v.AccountID, v.AccountID, v.AccountID, v.Admin}
	for _, t := range v.Teams {
		args = append(args, t)
	}
	args = append(args, v.Owner)
	return `(? OR (? != '' AND (account_id = ? OR id IN (SELECT session_id FROM session_shares WHERE account_id = ?)))
		OR visibility NOT IN ('team', 'private')
		OR (visibility = 'team' AND ((? AND substr(owner_team_id, 1, 9) != '` + personalPrefix + `')
			OR owner_team_id IN (` + placeholders(len(v.Teams)) + `)))
		OR (visibility = 'private' AND ? AND substr(owner_team_id, 1, 9) != '` + personalPrefix + `'))`, args
}

// ShareSession sets the people a session is shared with, and returns them.
func (s *Store) ShareSession(ctx context.Context, sessionID string, accountIDs []string) ([]string, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_shares WHERE session_id=?`, sessionID); err != nil {
		return nil, err
	}
	now := ms(time.Now())
	for _, id := range accountIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_shares (session_id, account_id, created_at)
			VALUES (?,?,?)`, sessionID, id, now); err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return nil, ErrNotFound
			}
			return nil, err
		}
	}
	var out string
	if err := tx.QueryRowContext(ctx, `SELECT `+sharedWithCol+` FROM sessions WHERE id=?`, sessionID).Scan(&out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return splitShares(out), tx.Commit()
}

// sharedWithCol lists, comma separated, whom a session is shared with.
const sharedWithCol = `(SELECT COALESCE(group_concat(sh.account_id, ','), '') FROM session_shares sh
	WHERE sh.session_id = sessions.id)`

func splitShares(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, ",")
	sort.Strings(out)
	return out
}

// ------------------------------------------------------------------- audit

// AuditEvent records a change to who may do what.
type AuditEvent struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	ActorID string          `json:"actor_id,omitempty"`
	Actor   string          `json:"actor"`
	TeamID  string          `json:"team_id,omitempty"`
	Action  string          `json:"action"`
	Target  string          `json:"target,omitempty"`
	Detail  json.RawMessage `json:"detail"`
}

// Audit records an event. Detail is marshalled to JSON; never pass secret
// values.
func (s *Store) Audit(ctx context.Context, e AuditEvent, detail any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO audit_events (at, actor_id, actor, team_id, action, target, detail)
		VALUES (?,?,?,?,?,?,?)`, ms(time.Now()), e.ActorID, e.Actor, e.TeamID, e.Action, e.Target, string(b))
	return err
}

// AuditEvents lists events newest first, optionally for one team, before
// an id (for paging).
func (s *Store) AuditEvents(ctx context.Context, teamID string, before int64, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q, args := `SELECT id, at, actor_id, actor, team_id, action, target, detail FROM audit_events WHERE 1=1`, []any{}
	if teamID != "" {
		q += ` AND team_id=?`
		args = append(args, teamID)
	}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	return query(ctx, s.r, func(r scanner) (AuditEvent, error) {
		var e AuditEvent
		var at int64
		var detail string
		err := r.Scan(&e.ID, &at, &e.ActorID, &e.Actor, &e.TeamID, &e.Action, &e.Target, &detail)
		e.At, e.Detail = fromMS(at), json.RawMessage(detail)
		return e, err
	}, q, args...)
}
