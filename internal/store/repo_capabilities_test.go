package store

import (
	"context"
	"slices"
	"testing"
)

func TestRepoCapabilities(t *testing.T) {
	s, ctx := open(t), context.Background()
	if got, err := s.RepoCapabilities(ctx, []string{"acme/app"}); err != nil || len(got) != 0 {
		t.Fatalf("unknown repository: %v, %v", got, err)
	}
	s.SetRepoCapabilities(ctx, "acme/app", []string{"go", "android"})
	s.SetRepoCapabilities(ctx, "acme/web", []string{"bun", "go"})
	if got, _ := s.RepoCapabilities(ctx, []string{"acme/app", "acme/web"}); !slices.Equal(got, []string{"go", "android", "bun"}) {
		t.Errorf("together = %v", got)
	}
	// A later boot replaces what a repository needs.
	s.SetRepoCapabilities(ctx, "acme/app", nil)
	if got, _ := s.RepoCapabilities(ctx, []string{"acme/app"}); len(got) != 0 {
		t.Errorf("after replacing = %v", got)
	}
}
