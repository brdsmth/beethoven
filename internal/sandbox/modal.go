package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Modal is a Provider backed by Modal Sandboxes (https://modal.com), for
// servers that can't run containers themselves.
//
// Modal sandboxes can't be paused, so Stop snapshots the filesystem,
// publishes the snapshot under the sandbox's name and ends the sandbox; the
// next Start creates a sandbox from that snapshot. Files survive a stop (the
// workspace, installed dependencies, the agent's conversation); processes
// don't, and nothing of Beethoven's runs between commands anyway. Snapshots
// expire after Modal's default 30 days, after which a session starts over
// from a fresh clone.
type Modal struct {
	api modalAPI

	mu       sync.Mutex
	switchTo map[string]bool // sandbox → commands must switch from root to the agent user
}

// modalAPI is what the provider needs from Modal; see modalClient.
type modalAPI interface {
	running(ctx context.Context, name string) (id string, ok bool, err error)
	create(ctx context.Context, name string, img modalImage, s Spec) (id string, err error)
	exec(ctx context.Context, id string, args []string, dir string, env map[string]string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
	nameOf(ctx context.Context, id string) (string, error)
	snapshot(ctx context.Context, id string) (imageID string, err error)
	terminate(ctx context.Context, id string) error
	published(ctx context.Context, name string) (imageID string, ok bool, err error)
	publish(ctx context.Context, imageID, name string) error
	deleteImage(ctx context.Context, imageID string) error
}

// modalImage is what a sandbox starts from: a snapshot, or a registry image
// with Dockerfile commands on top.
type modalImage struct {
	snapshot string
	registry string
	commands []string
}

// errNotFound is how a modalAPI reports that a sandbox or image doesn't exist.
var errNotFound = errors.New("not found")

// DefaultImage names the sandbox image built from this package's Dockerfile.
const DefaultImage = "beethoven-sandbox:latest"

// image resolves a Spec's image: the default one is built on Modal from
// the Dockerfile; any other name is pulled from its registry.
func (m *Modal) image(name string) modalImage {
	if name == "" || name == DefaultImage {
		base, commands := dockerfileCommands(string(Dockerfile))
		return modalImage{registry: base, commands: commands}
	}
	return modalImage{registry: name}
}

func (m *Modal) Start(ctx context.Context, s Spec) (string, bool, error) {
	if id, ok, err := m.api.running(ctx, s.Name); err != nil || ok {
		return id, false, err
	}
	img := m.image(s.Image)
	snap, ok, err := m.api.published(ctx, s.Name)
	if err != nil {
		return "", false, err
	}
	if ok {
		img = modalImage{snapshot: snap}
	}
	id, err := m.create(ctx, s, img)
	if err != nil && ok { // an expired or unusable snapshot: start over
		id, err = m.create(ctx, s, m.image(s.Image))
	}
	return id, err == nil, err
}

// create starts a sandbox, waiting out a previous one of the same name that
// is still shutting down.
func (m *Modal) create(ctx context.Context, s Spec, img modalImage) (string, error) {
	for attempt := 0; ; attempt++ {
		id, err := m.api.create(ctx, s.Name, img, s)
		if err == nil || !errors.Is(err, errAlreadyExists) || attempt == 10 {
			return id, err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(modalRetryEvery):
		}
	}
}

var modalRetryEvery = time.Second

// errAlreadyExists is how a modalAPI reports a name taken by another sandbox.
var errAlreadyExists = errors.New("already exists")

func (m *Modal) Exec(ctx context.Context, id string, c Cmd) (int, error) {
	root, err := m.mustSwitch(ctx, id)
	if err != nil {
		return -1, err
	}
	tag := randomTag()
	args := append([]string{"sh", "-c", wrapper, "sh"}, c.Args...)
	if root {
		args = append([]string{"runuser", "-u", "agent", "--"}, args...)
	}
	env := map[string]string{"BEETHOVEN_EXEC": tag}
	for k, v := range c.Env {
		env[k] = v
	}
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		// Not ctx: cancelling the call would leave the process running.
		code, err := m.api.exec(context.WithoutCancel(ctx), id, args, c.Dir, env, c.Stdin, c.Stdout, c.Stderr)
		done <- result{code, err}
	}()
	select {
	case r := <-done:
		return r.code, r.err
	case <-ctx.Done():
	}
	// Signal the process tree inside the sandbox: TERM, then KILL after a
	// grace period.
	kill := func(sig string) {
		kctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		m.api.exec(kctx, id, []string{"sh", "-c", killTree, "sh", tag, sig}, "", nil, nil, nil, nil)
	}
	kill("TERM")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		kill("KILL")
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	return -1, ctx.Err()
}

// mustSwitch reports whether commands in a sandbox run as root while the
// image has an agent user to run them as: Claude Code refuses to bypass
// its permission prompts as root.
func (m *Modal) mustSwitch(ctx context.Context, id string) (bool, error) {
	m.mu.Lock()
	v, ok := m.switchTo[id]
	m.mu.Unlock()
	if ok {
		return v, nil
	}
	var out bytes.Buffer
	_, err := m.api.exec(ctx, id, []string{"sh", "-c", `[ "$(id -u)" = 0 ] && id agent >/dev/null 2>&1 && echo switch; true`},
		"", nil, nil, &out, nil)
	if err != nil {
		return false, err
	}
	v = strings.TrimSpace(out.String()) == "switch"
	m.mu.Lock()
	if m.switchTo == nil {
		m.switchTo = map[string]bool{}
	}
	m.switchTo[id] = v
	m.mu.Unlock()
	return v, nil
}

func (m *Modal) Stop(ctx context.Context, id string) error {
	name, err := m.api.nameOf(ctx, id)
	if errors.Is(err, errNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	snap, err := m.api.snapshot(ctx, id)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err) // keep it running rather than lose its files
	}
	old, had, err := m.api.published(ctx, name)
	if err != nil {
		return err
	}
	if err := m.api.publish(ctx, snap, name); err != nil {
		return err
	}
	if had && old != snap {
		m.api.deleteImage(ctx, old) // best effort: it expires anyway
	}
	m.forget(id)
	return m.api.terminate(ctx, id)
}

func (m *Modal) Destroy(ctx context.Context, id string) error {
	name, err := m.api.nameOf(ctx, id)
	if errors.Is(err, errNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if snap, ok, err := m.api.published(ctx, name); err == nil && ok {
		m.api.deleteImage(ctx, snap)
	}
	m.forget(id)
	if err := m.api.terminate(ctx, id); !errors.Is(err, errNotFound) {
		return err
	}
	return nil
}

func (m *Modal) forget(id string) {
	m.mu.Lock()
	delete(m.switchTo, id)
	m.mu.Unlock()
}

// dockerfileCommands splits a Dockerfile into its base image and the
// instructions after FROM, one per element, comments dropped and
// continuation lines joined.
func dockerfileCommands(dockerfile string) (base string, commands []string) {
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			if base == "" && strings.HasPrefix(strings.ToUpper(s), "FROM ") {
				base = strings.TrimSpace(s[5:])
			} else {
				commands = append(commands, s)
			}
		}
		cur.Reset()
	}
	for _, line := range strings.Split(dockerfile, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasSuffix(t, "\\") {
			cur.WriteString(strings.TrimSuffix(t, "\\") + " ")
			continue
		}
		cur.WriteString(t)
		flush()
	}
	flush()
	return base, commands
}
