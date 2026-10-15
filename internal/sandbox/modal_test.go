package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeModal is an in-memory Modal: named sandboxes, snapshots, published
// images, and commands that understand just enough to test the provider.
type fakeModal struct {
	mu        sync.Mutex
	n         int
	sandboxes map[string]*fakeSandbox // by id
	pub       map[string]string       // name → published image
	images    map[string]bool
	deleted   []string
	broken    map[string]bool // images that fail to start a sandbox
	busy      int             // creates that find the name still taken
	root      bool            // commands run as root in an image with an agent user
	detects   int
	execs     [][]string
	kills     []string
	blocked   map[string]chan struct{} // running `sleep`s, by exec tag
}

type fakeSandbox struct {
	name    string
	running bool
	from    modalImage
}

func newFakeModal() *fakeModal {
	return &fakeModal{sandboxes: map[string]*fakeSandbox{}, pub: map[string]string{}, images: map[string]bool{},
		broken: map[string]bool{}, blocked: map[string]chan struct{}{}}
}

func (f *fakeModal) running(_ context.Context, name string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, sb := range f.sandboxes {
		if sb.name == name && sb.running {
			return id, true, nil
		}
	}
	return "", false, nil
}

func (f *fakeModal) create(_ context.Context, name string, img modalImage, _ Spec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy > 0 {
		f.busy--
		return "", errAlreadyExists
	}
	if f.broken[img.snapshot] {
		return "", errors.New("image expired")
	}
	f.n++
	id := fmt.Sprintf("sb-%d", f.n)
	f.sandboxes[id] = &fakeSandbox{name: name, running: true, from: img}
	return id, nil
}

func (f *fakeModal) exec(ctx context.Context, id string, args []string, _ string, env map[string]string, stdin io.Reader, stdout, _ io.Writer) (int, error) {
	f.mu.Lock()
	f.execs = append(f.execs, args)
	f.mu.Unlock()
	if stdout == nil {
		stdout = io.Discard
	}
	switch {
	case len(args) > 2 && strings.Contains(args[2], "id -u"):
		f.mu.Lock()
		f.detects++
		root := f.root
		f.mu.Unlock()
		if root {
			fmt.Fprintln(stdout, "switch")
		}
		return 0, nil
	case len(args) > 2 && args[2] == killTree:
		f.mu.Lock()
		f.kills = append(f.kills, args[5])
		if ch, ok := f.blocked[args[4]]; ok {
			close(ch)
			delete(f.blocked, args[4])
		}
		f.mu.Unlock()
		return 0, nil
	}
	// A command through the wrapper: [runuser -u agent --] sh -c <wrapper> sh <command...>
	i := slices.Index(args, wrapper)
	cmd := args[i+2:]
	switch cmd[0] {
	case "cat":
		io.Copy(stdout, stdin)
	case "sleep":
		ch := make(chan struct{})
		f.mu.Lock()
		f.blocked[env["BEETHOVEN_EXEC"]] = ch
		f.mu.Unlock()
		<-ch
		return 143, nil
	default:
		fmt.Fprintf(stdout, "ran %s", strings.Join(cmd, " "))
	}
	return 0, nil
}

func (f *fakeModal) nameOf(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sb, ok := f.sandboxes[id]; ok {
		return sb.name, nil
	}
	return "", errNotFound
}

func (f *fakeModal) snapshot(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	im := fmt.Sprintf("im-%d", f.n)
	f.images[im] = true
	return im, nil
}

func (f *fakeModal) terminate(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sb, ok := f.sandboxes[id]; ok {
		sb.running = false
		return nil
	}
	return errNotFound
}

func (f *fakeModal) published(_ context.Context, name string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	im, ok := f.pub[name]
	return im, ok, nil
}

func (f *fakeModal) publish(_ context.Context, imageID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pub[name] = imageID
	return nil
}

func (f *fakeModal) deleteImage(_ context.Context, imageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.images, imageID)
	f.deleted = append(f.deleted, imageID)
	return nil
}

var ctx = context.Background()

func TestModalStopsBySnapshotAndResumesFromIt(t *testing.T) {
	f := newFakeModal()
	m := &Modal{api: f}
	spec := Spec{Name: "beethoven-s1", Image: DefaultImage}

	id, started, err := m.Start(ctx, spec)
	if err != nil || !started {
		t.Fatalf("start: %q %v %v", id, started, err)
	}
	if from := f.sandboxes[id].from; from.registry != "node:24-bookworm-slim" || len(from.commands) == 0 {
		t.Errorf("created from %+v, want the Dockerfile built on Modal", from)
	}
	if again, started, _ := m.Start(ctx, spec); again != id || started {
		t.Errorf("start while running: %q, started %v", again, started)
	}
	var out bytes.Buffer
	if code, err := m.Exec(ctx, id, Cmd{Args: []string{"echo", "hi"}, Stdout: &out}); code != 0 || err != nil || out.String() != "ran echo hi" {
		t.Errorf("exec: %d %v %q", code, err, out.String())
	}

	if err := m.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	first := f.pub["beethoven-s1"]
	if f.sandboxes[id].running || first == "" {
		t.Fatalf("after stop: running %v, published %q", f.sandboxes[id].running, first)
	}
	resumed, started, err := m.Start(ctx, spec)
	if err != nil || !started || resumed == id || f.sandboxes[resumed].from.snapshot != first {
		t.Fatalf("resume: %q from %+v, %v", resumed, f.sandboxes[resumed].from, err)
	}
	m.Stop(ctx, resumed)
	if f.pub["beethoven-s1"] == first || !slices.Contains(f.deleted, first) {
		t.Errorf("second stop: published %q, deleted %v; want the old snapshot replaced and deleted", f.pub["beethoven-s1"], f.deleted)
	}
	if err := m.Stop(ctx, "sb-missing"); err != nil {
		t.Errorf("stopping a sandbox that's gone: %v", err)
	}
}

func TestModalStartsOverWhenTheSnapshotIsGone(t *testing.T) {
	f := newFakeModal()
	f.pub["beethoven-s1"], f.broken["im-old"] = "im-old", true
	m := &Modal{api: f}
	id, _, err := m.Start(ctx, Spec{Name: "beethoven-s1", Image: DefaultImage})
	if err != nil || f.sandboxes[id].from.registry == "" {
		t.Errorf("start: %v from %+v, want the base image", err, f.sandboxes[id])
	}
	id, _, _ = m.Start(ctx, Spec{Name: "beethoven-s2", Image: "ghcr.io/acme/tools:1"})
	if from := f.sandboxes[id].from; from.registry != "ghcr.io/acme/tools:1" || len(from.commands) != 0 {
		t.Errorf("custom image: %+v", from)
	}
}

func TestModalWaitsForANameStillInUse(t *testing.T) {
	modalRetryEvery = time.Millisecond
	defer func() { modalRetryEvery = time.Second }()
	f := newFakeModal()
	f.busy = 3
	if _, started, err := (&Modal{api: f}).Start(ctx, Spec{Name: "beethoven-s1"}); err != nil || !started {
		t.Errorf("start: %v", err)
	}
}

func TestModalExec(t *testing.T) {
	f := newFakeModal()
	f.root = true
	m := &Modal{api: f}
	id, _, _ := m.Start(ctx, Spec{Name: "beethoven-s1"})

	var out bytes.Buffer
	if _, err := m.Exec(ctx, id, Cmd{Args: []string{"cat"}, Stdin: strings.NewReader("the prompt"), Stdout: &out}); err != nil || out.String() != "the prompt" {
		t.Errorf("stdin: %v %q", err, out.String())
	}
	m.Exec(ctx, id, Cmd{Args: []string{"true"}})
	if f.detects != 1 {
		t.Errorf("checked the user %d times, want once per sandbox", f.detects)
	}
	if last := f.execs[len(f.execs)-1]; !slices.Equal(last[:4], []string{"runuser", "-u", "agent", "--"}) {
		t.Errorf("as root: %q, want it run as the agent user", last)
	}

	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	code, err := m.Exec(cctx, id, Cmd{Args: []string{"sleep", "600"}})
	if code != -1 || !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Errorf("cancel: %d %v after %s", code, err, time.Since(start))
	}
	if !slices.Equal(f.kills, []string{"TERM"}) {
		t.Errorf("signals %v, want the process tree terminated", f.kills)
	}
}

func TestDockerfileCommands(t *testing.T) {
	base, cmds := dockerfileCommands(string(Dockerfile))
	if base != "node:24-bookworm-slim" {
		t.Errorf("base %q", base)
	}
	if !slices.Contains(cmds, "USER agent") || !slices.Contains(cmds, "WORKDIR /workspace") {
		t.Errorf("commands %q", cmds)
	}
	for _, c := range cmds {
		if strings.Contains(c, "\\") || strings.HasPrefix(c, "#") {
			t.Errorf("unjoined or comment line: %q", c)
		}
	}
	if !strings.HasPrefix(cmds[0], "RUN apt-get update") || !strings.Contains(cmds[0], "rm -rf /var/lib/apt/lists/*") {
		t.Errorf("first command %q, want the whole RUN joined", cmds[0])
	}
}
