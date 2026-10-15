package sandbox

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestModalLive drives real Modal sandboxes. It runs only with
// BEETHOVEN_TEST_MODAL=1 and Modal credentials, and costs a few cents.
func TestModalLive(t *testing.T) {
	if os.Getenv("BEETHOVEN_TEST_MODAL") != "1" {
		t.Skip("set BEETHOVEN_TEST_MODAL=1 to use Modal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	m, err := NewModal("beethoven-test")
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Name: "beethoven-live-" + randomTag(), Image: DefaultImage, CPUs: 1, MemoryMB: 1024}
	run := func(id string, stdin string, args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		c := Cmd{Args: args, Stdout: &out, Stderr: &errb, Env: map[string]string{"SECRET_THING": "s3cret"}}
		if stdin != "" {
			c.Stdin = strings.NewReader(stdin)
		}
		if code, err := m.Exec(ctx, id, c); err != nil || code != 0 {
			t.Fatalf("%v: exit %d, %v: %s", args, code, err, errb.String())
		}
		return strings.TrimSpace(out.String())
	}

	id, _, err := m.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Destroy(context.Background(), id) })
	if who := run(id, "", "id", "-un"); who != "agent" {
		t.Errorf("commands run as %q, want agent", who)
	}
	if v := run(id, "", "claude", "--version"); !strings.Contains(v, "Claude Code") {
		t.Errorf("claude --version = %q", v)
	}
	if got := run(id, "the prompt", "cat"); got != "the prompt" {
		t.Errorf("stdin: %q", got)
	}
	if got := run(id, "", "sh", "-c", `echo "$SECRET_THING" > /workspace/kept && cat /workspace/kept`); got != "s3cret" {
		t.Errorf("env: %q", got)
	}

	start := time.Now()
	if err := m.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	t.Logf("stop (snapshot) took %s", time.Since(start))
	start = time.Now()
	resumed, started, err := m.Start(ctx, spec)
	if err != nil || !started {
		t.Fatalf("resume: %v", err)
	}
	id = resumed
	t.Logf("resume took %s", time.Since(start))
	if got := run(id, "", "cat", "/workspace/kept"); got != "s3cret" {
		t.Errorf("after resuming: %q, want the file kept", got)
	}

	cctx, stop := context.WithCancel(ctx)
	go func() { time.Sleep(3 * time.Second); stop() }()
	if code, _ := m.Exec(cctx, id, Cmd{Args: []string{"sh", "-c", "sleep 300 & sleep 300"}}); code != -1 {
		t.Errorf("cancel: exit %d", code)
	}
	// Not every sleep: the sandbox's own process is `sleep infinity`.
	if left := run(id, "", "sh", "-c", "pgrep -c -f '^sleep 300' || true"); left != "0" {
		t.Errorf("%s sleep processes left after cancelling", left)
	}
}
