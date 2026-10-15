// Package sandbox runs commands inside isolated, persistent environments.
//
// The contract is deliberately small: Start brings a sandbox up (creating it
// or resuming it), Exec runs a process in it, Stop halts it while keeping its
// filesystem, and Destroy removes it. Whether a provider keeps state by
// pausing a container or by snapshotting and restoring is its own business;
// callers never see the difference.
package sandbox

import (
	"context"
	"errors"
	"io"
)

type Spec struct {
	Name     string            // stable identity, e.g. derived from the session id
	Image    string            // base image
	Env      map[string]string // baked in at creation; must not hold secrets
	CPUs     float64           // 0 = provider default
	MemoryMB int               // 0 = provider default
}

type Cmd struct {
	Args   []string
	Env    map[string]string // per-process; the place for secrets
	Dir    string
	Stdin  io.Reader
	// Stdout and Stderr may be the same writer, which then gets one Write
	// at a time, as with os/exec.
	Stdout io.Writer
	Stderr io.Writer
}

type Provider interface {
	// Start creates the sandbox, or resumes it if it exists. It is idempotent
	// and returns the provider's handle; started is false only when the
	// sandbox was already running.
	Start(ctx context.Context, s Spec) (id string, started bool, err error)
	// Exec runs c to completion and returns its exit code. Cancelling ctx
	// terminates the process and everything it started.
	Exec(ctx context.Context, id string, c Cmd) (exit int, err error)
	// Stop halts the sandbox, preserving its filesystem for the next Start.
	Stop(ctx context.Context, id string) error
	// Destroy removes the sandbox and its filesystem.
	Destroy(ctx context.Context, id string) error
}

// Unavailable is the Provider of a server with no way to run sandboxes:
// sessions fail with Reason until one is configured.
type Unavailable struct{ Reason string }

func (u Unavailable) Start(context.Context, Spec) (string, bool, error) {
	return "", false, errors.New(u.Reason)
}
func (u Unavailable) Exec(context.Context, string, Cmd) (int, error) { return -1, errors.New(u.Reason) }
func (u Unavailable) Stop(context.Context, string) error             { return nil }
func (u Unavailable) Destroy(context.Context, string) error          { return nil }
