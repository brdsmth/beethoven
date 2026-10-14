package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Dockerfile builds the default sandbox image.
//
//go:embed Dockerfile
var Dockerfile []byte

// Docker is a Provider backed by local containers driven through the docker
// CLI. A sandbox is one long-lived container; Stop is `docker stop`, which
// keeps the container's filesystem for the next Start.
type Docker struct {
	Binary string   // docker CLI; default "docker"
	Args   []string // extra `docker run` flags, e.g. resource limits or mounts
}

func (d *Docker) bin() string {
	if d.Binary == "" {
		return "docker"
	}
	return d.Binary
}

func (d *Docker) Start(ctx context.Context, s Spec) (string, bool, error) {
	out, err := d.run(ctx, nil, "inspect", "-f", "{{.State.Running}}", s.Name)
	switch {
	case err == nil && strings.TrimSpace(out) == "true":
		return s.Name, false, nil
	case err == nil:
		_, err = d.run(ctx, nil, "start", s.Name)
		return s.Name, true, err
	case !notFound(err):
		return "", false, err
	}
	args := []string{"run", "-d", "--init", "--name", s.Name, "--label", "beethoven=1",
		"--add-host", "host.docker.internal:host-gateway"}
	for _, k := range sortedKeys(s.Env) {
		args = append(args, "-e", k+"="+s.Env[k])
	}
	if s.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(s.CPUs, 'f', -1, 64))
	}
	if s.MemoryMB > 0 {
		args = append(args, "--memory", strconv.Itoa(s.MemoryMB)+"m")
	}
	args = append(args, d.Args...)
	args = append(args, s.Image, "sleep", "infinity")
	_, err = d.run(ctx, nil, args...)
	return s.Name, true, err
}

// wrapper records the process id so that cancellation can find it.
const wrapper = `echo $$ > /tmp/.beethoven-exec-$BEETHOVEN_EXEC; exec "$@"`

// killTree signals a recorded process and all of its descendants. The whole
// tree is collected before signalling so that orphans re-parented to init
// are not missed.
const killTree = `tree() { echo $1; for p in /proc/[0-9]*; do
  pp=$(sed "s/.*) //" $p/stat 2>/dev/null | cut -d" " -f2); [ "$pp" = "$1" ] && tree ${p#/proc/}; done; }
f=/tmp/.beethoven-exec-$1; [ -f $f ] && kill -$2 $(tree $(cat $f)) 2>/dev/null; true`

func (d *Docker) Exec(ctx context.Context, id string, c Cmd) (int, error) {
	tag := randomTag()
	args := []string{"exec", "-e", "BEETHOVEN_EXEC=" + tag}
	if c.Stdin != nil {
		args = append(args, "-i")
	}
	if c.Dir != "" {
		args = append(args, "-w", c.Dir)
	}
	// Values travel through the CLI's environment, not its argv, so secrets
	// never show up in the host's process list.
	env := os.Environ()
	for _, k := range sortedKeys(c.Env) {
		args = append(args, "-e", k)
		env = append(env, k+"="+c.Env[k])
	}
	args = append(args, id, "sh", "-c", wrapper, "sh")
	args = append(args, c.Args...)

	cmd := exec.Command(d.bin(), args...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, c.Stdin, c.Stdout, c.Stderr
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return exitCode(err)
	case <-ctx.Done():
	}
	// Killing the CLI would leave the process running in the container, so
	// signal the process tree inside it: TERM, then KILL after a grace period.
	bg := context.Background()
	d.run(bg, nil, "exec", id, "sh", "-c", killTree, "sh", tag, "TERM")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		d.run(bg, nil, "exec", id, "sh", "-c", killTree, "sh", tag, "KILL")
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}
	return -1, ctx.Err()
}

func (d *Docker) Stop(ctx context.Context, id string) error {
	_, err := d.run(ctx, nil, "stop", "-t", "10", id)
	if notFound(err) {
		return nil
	}
	return err
}

func (d *Docker) Destroy(ctx context.Context, id string) error {
	_, err := d.run(ctx, nil, "rm", "-f", "-v", id)
	if notFound(err) {
		return nil
	}
	return err
}

// EnsureImage builds tag from the given Dockerfile contents if it is missing.
func (d *Docker) EnsureImage(ctx context.Context, tag string, dockerfile []byte, log io.Writer) error {
	if _, err := d.run(ctx, nil, "image", "inspect", tag); err == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, d.bin(), "build", "-t", tag, "-")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(dockerfile), log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build %s: %w", tag, err)
	}
	return nil
}

func (d *Docker) run(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, d.bin(), args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func notFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such")
}

func exitCode(err error) (int, error) {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func randomTag() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
