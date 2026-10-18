package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/brdsmth/beethoven/internal/capability"
	"github.com/brdsmth/beethoven/internal/sandbox"
)

// fileBox is a sandbox holding files, which serves `cat "$FILE"`.
type fileBox struct {
	sandbox.Unavailable
	files map[string][]byte
}

func (b *fileBox) Exec(_ context.Context, _ string, c sandbox.Cmd) (int, error) {
	data, ok := b.files[c.Env["FILE"]]
	if !ok {
		return 0, nil // the cleanup
	}
	c.Stdout.Write(data)
	return 0, nil
}

func TestKeepExports(t *testing.T) {
	saved := capability.Catalog
	t.Cleanup(func() { capability.Catalog = saved })
	capability.Catalog = []capability.Capability{{ID: "kit-two", Name: "Kit", Install: "true", Paths: []string{"kit"}}}
	c := capability.Catalog[0]

	dir := t.TempDir()
	const file = "/home/agent/.beethoven/export/kit-two.tar.gz"
	box := &fileBox{files: map[string][]byte{file: []byte("packed")}}
	r := &runner{m: &Manager{cfg: Config{CacheDir: dir}, box: box, log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	sum := sha256.Sum256([]byte("packed"))
	good := capabilityExport{ID: "kit-two", Hash: c.Hash(), Machine: "x86_64", SHA256: hex.EncodeToString(sum[:]), File: file}
	kept := func() []string {
		entries, _ := os.ReadDir(dir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}

	for name, bad := range map[string]func(e *capabilityExport){
		"checksum":     func(e *capabilityExport) { e.SHA256 = hex.EncodeToString(make([]byte, 32)) },
		"version":      func(e *capabilityExport) { e.Hash = "000000000000" },
		"capability":   func(e *capabilityExport) { e.ID = "nope" },
		"machine":      func(e *capabilityExport) { e.Machine = "../x" },
		"another file": func(e *capabilityExport) { e.File = "/etc/passwd" },
	} {
		e := good
		bad(&e)
		r.keepExports(context.Background(), "box", []capabilityExport{e}, "img")
		if got := kept(); len(got) != 0 {
			t.Errorf("kept an export with a bad %s: %v", name, got)
		}
	}

	// An older version of the same capability, machine and image goes; one
	// for another image, and another capability whose id starts the same,
	// stay.
	older := cacheName("kit-two", "aaaaaaaaaaaa", "x86_64", "img")
	other := cacheName("kit-two", "aaaaaaaaaaaa", "x86_64", "other-img")
	lookalike := cacheName("kit", "aaaaaaaaaaaa", "x86_64", "img")
	for _, n := range []string{older, other, lookalike} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o600)
	}
	r.keepExports(context.Background(), "box", []capabilityExport{good}, "img")
	want := []string{cacheName("kit-two", c.Hash(), "x86_64", "img"), other, lookalike}
	slices.Sort(want)
	if got := kept(); !slices.Equal(got, want) {
		t.Errorf("kept %v, want %v", got, want)
	}
	data, _ := os.ReadFile(filepath.Join(dir, want[slices.Index(want, cacheName("kit-two", c.Hash(), "x86_64", "img"))]))
	if string(data) != "packed" {
		t.Errorf("kept %q", data)
	}

	// Expired installs are pruned.
	stale := filepath.Join(dir, other)
	old := time.Now().Add(-keptFor - time.Hour)
	os.Chtimes(stale, old, old)
	pruneKept(dir)
	if _, err := os.Stat(stale); err == nil {
		t.Error("an expired install was kept")
	}
}

func TestBootPhasesStopTrustingCapabilityLines(t *testing.T) {
	var b bootPhases
	for _, p := range []string{"boot", "clone app", "capability go"} {
		b.set(p)
		if !b.capabilities() {
			t.Errorf("%q: capability lines should count", p)
		}
	}
	// A repository's hook prints a capability phase: it doesn't count.
	for _, p := range []string{"setup app", "capability go", "boot"} {
		b.set(p)
		if b.capabilities() {
			t.Errorf("after the hooks, %q counted", p)
		}
	}
}

func TestRepoLinesNameKnownCapabilities(t *testing.T) {
	var c capabilityLines
	c.line("::repo app go")
	c.line("::repo app made-up")
	if got := c.repos["app"]; !slices.Equal(got, []string{"go"}) {
		t.Errorf("repos = %v", got)
	}
}
