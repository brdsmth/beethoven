package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// RepoCapabilities returns the capabilities repos named or looked like they
// needed when a sandbox last booted with them, together and once each.
func (s *Store) RepoCapabilities(ctx context.Context, repos []string) ([]string, error) {
	var out []string
	for _, repo := range repos {
		var ids string
		err := s.r.QueryRowContext(ctx, `SELECT ids FROM repo_capabilities WHERE repo=?`, repo).Scan(&ids)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		var list []string
		json.Unmarshal([]byte(ids), &list)
		for _, id := range list {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// SetRepoCapabilities records what a repository needs, replacing what it
// needed before.
func (s *Store) SetRepoCapabilities(ctx context.Context, repo string, ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	b, _ := json.Marshal(ids)
	_, err := s.w.ExecContext(ctx, `INSERT INTO repo_capabilities (repo, ids, updated_at) VALUES (?,?,?)
		ON CONFLICT (repo) DO UPDATE SET ids=excluded.ids, updated_at=excluded.updated_at`, repo, string(b), ms(time.Now()))
	return err
}
