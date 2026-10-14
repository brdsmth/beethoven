package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func open(t *testing.T) *Store {
	t.Helper()
	key := []byte(strings.Repeat("k", 32))
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func session(t *testing.T, s *Store) Session {
	t.Helper()
	x := Session{ID: NewID(), Title: "t", Branch: "b", Source: "test", Status: "idle", Sandbox: "none"}
	if err := s.CreateSession(ctx, &x); err != nil {
		t.Fatal(err)
	}
	return x
}

func TestQueueOrderAndClaim(t *testing.T) {
	s := open(t)
	x := session(t, s)
	var ids []string
	for range 3 {
		p := Prompt{ID: NewID(), SessionID: x.ID, Content: "c", Source: "test"}
		if _, err := s.CreatePrompt(ctx, &p); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	for _, want := range ids {
		p, err := s.ClaimNext(ctx, x.ID)
		if err != nil || p.ID != want || p.Status != Running || p.StartedAt == nil {
			t.Fatalf("ClaimNext = %+v, %v; want %s", p, err, want)
		}
	}
	if _, err := s.ClaimNext(ctx, x.ID); err != ErrNotFound {
		t.Errorf("empty queue: err = %v", err)
	}
}

func TestDedupAndCancel(t *testing.T) {
	s := open(t)
	x := session(t, s)
	a := Prompt{ID: NewID(), SessionID: x.ID, Content: "a", Source: "test", DedupKey: "k1"}
	b := Prompt{ID: NewID(), SessionID: x.ID, Content: "b", Source: "test", DedupKey: "k1"}
	if created, _ := s.CreatePrompt(ctx, &a); !created {
		t.Fatal("first prompt not created")
	}
	if created, _ := s.CreatePrompt(ctx, &b); created || b.ID != a.ID || b.Content != "a" {
		t.Fatalf("duplicate: created=%v prompt=%+v", created, b)
	}
	c := Prompt{ID: NewID(), SessionID: x.ID, Content: "c", Source: "test"}
	s.CreatePrompt(ctx, &c)
	got, err := s.CancelQueued(ctx, x.ID, c.ID)
	if err != nil || len(got) != 1 || got[0].ID != c.ID || got[0].Status != Cancelled {
		t.Fatalf("cancel one = %+v, %v", got, err)
	}
	got, _ = s.CancelQueued(ctx, x.ID, "")
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("cancel all = %+v", got)
	}
}

func TestEventSequenceIsDensePerSession(t *testing.T) {
	s := open(t)
	x, y := session(t, s), session(t, s)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := x.ID
			if i%2 == 1 {
				id = y.ID
			}
			e := Event{Session: id, Type: "t", Data: []byte(`{}`), At: time.Now()}
			if err := s.AppendEvent(ctx, &e); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for _, id := range []string{x.ID, y.ID} {
		evs, _ := s.Events(ctx, id, 0, 100)
		for i, e := range evs {
			if e.Seq != int64(i+1) {
				t.Fatalf("session %s event %d has seq %d", id, i, e.Seq)
			}
		}
	}
	if evs, _ := s.Events(ctx, x.ID, 20, 100); len(evs) != 5 || evs[0].Seq != 21 {
		t.Errorf("Events(after=20) = %d events starting at %v", len(evs), evs)
	}
}

func TestSecretsAreEncryptedAndLayered(t *testing.T) {
	s := open(t)
	s.SetSecret(ctx, "", "TOKEN", "global-value")
	s.SetSecret(ctx, "", "ONLY_GLOBAL", "g")
	s.SetSecret(ctx, "acme/app", "TOKEN", "repo-value")
	s.SetSecret(ctx, "acme/other", "TOKEN", "other-value")

	env, err := s.SecretEnv(ctx, SecretTarget{Repos: []string{"acme/app"}})
	if err != nil {
		t.Fatal(err)
	}
	if env["TOKEN"] != "repo-value" || env["ONLY_GLOBAL"] != "g" || len(env) != 2 {
		t.Errorf("env = %v", env)
	}
	var raw []byte
	s.r.QueryRow(`SELECT value FROM secrets WHERE scope='' AND name='TOKEN'`).Scan(&raw)
	if strings.Contains(string(raw), "global-value") {
		t.Error("secret stored in plaintext")
	}
	wrongKey := &Store{r: s.r, w: s.w, key: []byte(strings.Repeat("x", 32))}
	if _, err := wrongKey.SecretEnv(ctx, SecretTarget{}); err == nil {
		t.Error("decrypted with the wrong key")
	}
}

func TestAdvanceScheduleFiresOnce(t *testing.T) {
	s := open(t)
	due := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	a := Automation{ID: NewID(), Name: "n", Prompt: "p", Schedule: "* * * * *", Enabled: true, NextRunAt: &due, HookHash: "h"}
	if err := s.CreateAutomation(ctx, &a); err != nil {
		t.Fatal(err)
	}
	list, _ := s.DueAutomations(ctx, time.Now())
	if len(list) != 1 {
		t.Fatalf("due = %d", len(list))
	}
	next := time.Now().Add(time.Minute)
	if ok, _ := s.AdvanceSchedule(ctx, a.ID, *list[0].NextRunAt, next); !ok {
		t.Fatal("first advance failed")
	}
	if ok, _ := s.AdvanceSchedule(ctx, a.ID, *list[0].NextRunAt, next); ok {
		t.Fatal("second advance from the same due time succeeded")
	}
}
