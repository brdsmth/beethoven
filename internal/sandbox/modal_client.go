package sandbox

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	modal "github.com/modal-labs/modal-client/go"
)

// NewModal connects to Modal with MODAL_TOKEN_ID and MODAL_TOKEN_SECRET (or
// ~/.modal.toml). Sandboxes belong to the named Modal app, created if
// missing.
func NewModal(app string) (*Modal, error) {
	mc, err := modal.NewClient()
	if err != nil {
		return nil, err
	}
	return &Modal{api: &modalClient{mc: mc, appName: app}}, nil
}

// Warm builds the default image ahead of the first session, which would
// otherwise wait for it.
func (m *Modal) Warm(ctx context.Context) error {
	c, ok := m.api.(*modalClient)
	if !ok {
		return nil
	}
	_, err := c.build(ctx, m.image(DefaultImage))
	return err
}

// modalClient is modalAPI over Modal's Go SDK.
type modalClient struct {
	mc      *modal.Client
	appName string

	mu     sync.Mutex
	app    *modal.App
	images map[string]*modal.Image // built images, by registry and commands
}

func (c *modalClient) appFor(ctx context.Context) (*modal.App, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.app == nil {
		app, err := c.mc.Apps.FromName(ctx, c.appName, &modal.AppFromNameParams{CreateIfMissing: true})
		if err != nil {
			return nil, err
		}
		c.app = app
	}
	return c.app, nil
}

// build resolves and builds an image once per process.
func (c *modalClient) build(ctx context.Context, img modalImage) (*modal.Image, error) {
	if img.snapshot != "" {
		return c.mc.Images.FromID(ctx, img.snapshot, nil)
	}
	key := img.registry
	for _, cmd := range img.commands {
		key += "\n" + cmd
	}
	c.mu.Lock()
	built, ok := c.images[key]
	c.mu.Unlock()
	if ok {
		return built, nil
	}
	app, err := c.appFor(ctx)
	if err != nil {
		return nil, err
	}
	im := c.mc.Images.FromRegistry(img.registry, nil)
	if len(img.commands) > 0 {
		im = im.DockerfileCommands(img.commands, nil)
	}
	if built, err = im.Build(ctx, app, nil); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.images == nil {
		c.images = map[string]*modal.Image{}
	}
	c.images[key] = built
	c.mu.Unlock()
	return built, nil
}

func (c *modalClient) running(ctx context.Context, name string) (string, bool, error) {
	sb, err := c.mc.Sandboxes.FromName(ctx, c.appName, name, nil)
	if err != nil {
		return "", false, translate(err, true)
	}
	defer sb.Detach()
	code, err := sb.Poll(ctx, nil)
	if err != nil {
		return "", false, err
	}
	return sb.SandboxID, code == nil, nil
}

func (c *modalClient) create(ctx context.Context, name string, img modalImage, s Spec) (string, error) {
	app, err := c.appFor(ctx)
	if err != nil {
		return "", err
	}
	im, err := c.build(ctx, img)
	if err != nil {
		return "", err
	}
	mem := s.MemoryMB
	sb, err := c.mc.Sandboxes.Create(ctx, app, im, &modal.SandboxCreateParams{
		Name:    name,
		Command: []string{"sleep", "infinity"},
		Timeout: 24 * time.Hour, // Modal's maximum
		// A backstop if this server goes away; it stops idle sandboxes, with
		// a snapshot, long before.
		IdleTimeout: 2 * time.Hour,
		CPU:         s.CPUs,
		MemoryMiB:   mem,
		Env:         s.Env,
		Tags:        map[string]string{"beethoven": "1", "name": name},
	})
	if err != nil {
		return "", translate(err, false)
	}
	defer sb.Detach()
	return sb.SandboxID, nil
}

func (c *modalClient) exec(ctx context.Context, id string, args []string, dir string, env map[string]string,
	stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	sb, err := c.mc.Sandboxes.FromID(ctx, id, nil)
	if err != nil {
		return -1, translate(err, false)
	}
	defer sb.Detach()
	p, err := sb.Exec(ctx, args, &modal.SandboxExecParams{Stdout: modal.Pipe, Stderr: modal.Pipe, Workdir: dir, Env: env})
	if err != nil {
		return -1, err
	}
	copied := copyOutput(stdout, stderr, p.Stdout, p.Stderr)
	if stdin != nil {
		go func() {
			io.Copy(p.Stdin, stdin)
			p.Stdin.Close()
		}()
	} else {
		p.Stdin.Close()
	}
	code, err := p.Wait(ctx, nil)
	copied.Wait()
	return code, err
}

// copyOutput copies a process's stdout and stderr as they arrive. As with
// os/exec, when both go to the same writer, one goroutine at a time writes
// to it: callers pass one writer for both and expect no interleaved Writes.
func copyOutput(stdout, stderr io.Writer, outR, errR io.Reader) *sync.WaitGroup {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if sameWriter(stdout, stderr) {
		w := &lockedWriter{w: stdout}
		stdout, stderr = w, w
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(stdout, outR) }()
	go func() { defer wg.Done(); io.Copy(stderr, errR) }()
	return &wg
}

// sameWriter compares writers as os/exec does: writers of an uncomparable
// type are never the same.
func sameWriter(a, b io.Writer) (same bool) {
	defer func() { recover() }()
	return a == b
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (c *modalClient) nameOf(ctx context.Context, id string) (string, error) {
	sb, err := c.mc.Sandboxes.FromID(ctx, id, nil)
	if err != nil {
		return "", translate(err, false)
	}
	defer sb.Detach()
	tags, err := sb.GetTags(ctx, nil)
	if err != nil {
		return "", translate(err, false)
	}
	if tags["name"] == "" {
		return "", errNotFound
	}
	return tags["name"], nil
}

func (c *modalClient) snapshot(ctx context.Context, id string) (string, error) {
	sb, err := c.mc.Sandboxes.FromID(ctx, id, nil)
	if err != nil {
		return "", translate(err, false)
	}
	defer sb.Detach()
	im, err := sb.SnapshotFilesystem(ctx, &modal.SandboxSnapshotFilesystemParams{Timeout: 5 * time.Minute})
	if err != nil {
		return "", err
	}
	return im.ImageID, nil
}

func (c *modalClient) terminate(ctx context.Context, id string) error {
	sb, err := c.mc.Sandboxes.FromID(ctx, id, nil)
	if err != nil {
		return translate(err, false)
	}
	defer sb.Detach()
	// Wait: until it has ended, its name still finds it, running, and a
	// Start right after a Stop would reuse it.
	_, err = sb.Terminate(ctx, &modal.SandboxTerminateParams{Wait: true})
	return translate(err, false)
}

func (c *modalClient) published(ctx context.Context, name string) (string, bool, error) {
	im, err := c.mc.Images.FromName(ctx, name, nil)
	if err != nil {
		if err := translate(err, true); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	return im.ImageID, true, nil
}

func (c *modalClient) publish(ctx context.Context, imageID, name string) error {
	im, err := c.mc.Images.FromID(ctx, imageID, nil)
	if err != nil {
		return err
	}
	return im.Publish(ctx, name, nil)
}

func (c *modalClient) deleteImage(ctx context.Context, imageID string) error {
	return c.mc.Images.Delete(ctx, imageID, nil)
}

// translate maps the SDK's errors onto the provider's; with missingOK, a
// missing resource is no error at all.
func translate(err error, missingOK bool) error {
	var nf modal.NotFoundError
	var ae modal.AlreadyExistsError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &nf) && missingOK:
		return nil
	case errors.As(err, &nf):
		return errNotFound
	case errors.As(err, &ae):
		return errAlreadyExists
	}
	return err
}
