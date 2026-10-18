package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/brdsmth/beethoven/internal/capability"
	"github.com/brdsmth/beethoven/internal/sandbox"
)

// Installed capabilities are kept on the server, so a sandbox gets a
// toolchain by unpacking it rather than downloading and building it again.
// A kept install is the capability's Paths, packed by the sandbox that
// first installed it, and is named by the capability's version, the
// machine and the image. Only a brand-new sandbox packs one, and only
// before any of the repositories' code runs (BOOT_STAGE=install): what the
// agent or a repository can touch never reaches other sessions.

// capabilityExport is a fresh install a sandbox packed: a "::cache" line.
type capabilityExport struct {
	ID, Hash, Machine, SHA256, File string
}

var machinePattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// cacheName names a kept install.
func cacheName(id, hash, machine, image string) string {
	img := sha256.Sum256([]byte(image))
	return fmt.Sprintf("%s-%s-%s-%s.tar.gz", id, hash, machine, hex.EncodeToString(img[:4]))
}

// unpackCached unpacks the kept installs of ids into a new sandbox and
// returns the "id-hash" of each, for CAP_CACHED. Each capability's Install
// still runs afterwards, to link and check what was unpacked.
func (r *runner) unpackCached(ctx context.Context, box string, ids []string, image string) []string {
	dir := r.m.cfg.CacheDir
	if dir == "" || len(ids) == 0 {
		return nil
	}
	var mach bytes.Buffer
	if code, err := r.m.box.Exec(ctx, box, sandbox.Cmd{Args: []string{"uname", "-m"}, Stdout: &mach}); err != nil || code != 0 {
		return nil
	}
	machine := strings.TrimSpace(mach.String())
	var done []string
	for _, id := range ids {
		c, ok := capability.Get(id)
		if !ok || len(c.Paths) == 0 {
			continue
		}
		f, err := os.Open(filepath.Join(dir, cacheName(id, c.Hash(), machine, image)))
		if err != nil {
			continue
		}
		r.setSandbox("starting", "capability "+id, "")
		errs := &tail{max: 2 << 10}
		// A partial unpack is removed, so Install starts from nothing.
		code, err := r.m.box.Exec(ctx, box, sandbox.Cmd{Args: []string{"sh", "-c", `cd "$HOME" && { tar -xzf - || { rm -rf $PATHS; exit 1; }; }`},
			Env: map[string]string{"PATHS": strings.Join(c.Paths, " ")}, Stdin: f, Stderr: errs})
		f.Close()
		if err != nil || code != 0 {
			r.m.log.Warn("unpacking a kept capability failed", "capability", id, "err", err, "stderr", strings.TrimSpace(errs.String()))
			continue
		}
		done = append(done, id+"-"+c.Hash())
	}
	return done
}

// keepExports copies the installs a new sandbox packed out of it, checking
// each against the checksum it reported, and replaces older versions of
// them.
func (r *runner) keepExports(ctx context.Context, box string, exports []capabilityExport, image string) {
	dir := r.m.cfg.CacheDir
	if dir == "" || len(exports) == 0 {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		r.m.log.Warn("can't keep capabilities", "err", err)
		return
	}
	for _, e := range exports {
		c, ok := capability.Get(e.ID)
		if !ok || c.Hash() != e.Hash || !machinePattern.MatchString(e.Machine) || !strings.HasSuffix(e.File, "/.beethoven/export/"+e.ID+".tar.gz") {
			continue
		}
		if err := r.keep(ctx, box, dir, e, image); err != nil {
			r.m.log.Warn("keeping a capability failed", "capability", e.ID, "err", err)
		}
	}
	r.m.box.Exec(ctx, box, sandbox.Cmd{Args: []string{"sh", "-c", `rm -rf "$HOME/.beethoven/export"`}})
}

func (r *runner) keep(ctx context.Context, box, dir string, e capabilityExport, image string) error {
	tmp, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // after the rename, there is nothing to remove
	h := sha256.New()
	code, err := r.m.box.Exec(ctx, box, sandbox.Cmd{Args: []string{"cat", e.File}, Stdout: io.MultiWriter(tmp, h)})
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return err
	case code != 0:
		return fmt.Errorf("reading %s: exit %d", e.File, code)
	case hex.EncodeToString(h.Sum(nil)) != e.SHA256:
		return fmt.Errorf("%s changed after it was packed", e.File)
	}
	name := cacheName(e.ID, e.Hash, e.Machine, image)
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	// Older versions of it, for the same machine and image.
	_, rest, _ := strings.Cut(strings.TrimPrefix(name, e.ID+"-"), "-")
	older := regexp.MustCompile(`^` + regexp.QuoteMeta(e.ID) + `-[0-9a-f]+-` + regexp.QuoteMeta(rest) + `$`)
	entries, _ := os.ReadDir(dir)
	for _, f := range entries {
		if f.Name() != name && older.MatchString(f.Name()) {
			os.Remove(filepath.Join(dir, f.Name()))
		}
	}
	return nil
}
