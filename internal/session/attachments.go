package session

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/store"
)

// Limits on the files attached to one prompt.
const (
	MaxAttachments     = 10
	MaxAttachmentBytes = 50 << 20 // in all
)

// AttachmentLimits says what the limits are, for error messages.
var AttachmentLimits = fmt.Sprintf("a prompt can have up to %d attached files, %d MB in all", MaxAttachments, MaxAttachmentBytes>>20)

// prepareAttachments checks a prompt's files against the limits and settles
// their names and types: each name becomes a file name of its own within the
// prompt, and each type is what the content shows it to be, where it shows.
func prepareAttachments(files []store.Attachment) ([]store.Attachment, error) {
	var total int
	for _, f := range files {
		total += len(f.Data)
	}
	if len(files) > MaxAttachments || total > MaxAttachmentBytes {
		return nil, fmt.Errorf("%w: %s", ErrInvalid, AttachmentLimits)
	}
	out := make([]store.Attachment, len(files))
	taken := map[string]bool{}
	for i, f := range files {
		base := fileName(f.Name)
		name := base
		for n := 2; taken[strings.ToLower(name)]; n++ {
			ext := path.Ext(base)
			name = strings.TrimSuffix(base, ext) + fmt.Sprintf("-%d", n) + ext
		}
		taken[strings.ToLower(name)] = true
		out[i] = store.Attachment{Name: name, ContentType: contentType(name, f.ContentType, f.Data), Data: f.Data}
	}
	return out, nil
}

// fileName makes a name safe to use as a file name: no directories, no
// control characters, not hidden, and at most 100 bytes, keeping the
// extension.
func fileName(name string) string {
	name = path.Base(strings.ReplaceAll(strings.ToValidUTF8(name, ""), `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if name == "" {
		return "file"
	}
	if len(name) > 100 {
		ext := path.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		stem := name[:100-len(ext)]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
		name = stem + ext
	}
	return name
}

// contentType is the type the content shows, else the one the extension
// implies, else the one declared.
func contentType(name, declared string, data []byte) string {
	sniffed := http.DetectContentType(data)
	if sniffed != "application/octet-stream" && !strings.HasPrefix(sniffed, "text/plain") {
		return sniffed
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	if t, params, err := mime.ParseMediaType(declared); err == nil && strings.Contains(t, "/") {
		if t = mime.FormatMediaType(t, params); t != "" {
			return t
		}
	}
	return sniffed
}

// attachScript saves its stdin as $NAME in the prompt's attachment
// directory, under the agent's home, and prints the directory.
const attachScript = `set -e
dir="${HOME:-/tmp}/attachments/$PROMPT"
mkdir -p "$dir"
cat > "$dir/$NAME"
echo "$dir"`

// attach copies a prompt's files into the sandbox and returns where they are.
func (r *runner) attach(ctx context.Context, box, promptID string, files []store.Attachment) (string, error) {
	var dir string
	for _, f := range files {
		out, errs := &tail{max: 4 << 10}, &tail{max: 4 << 10}
		code, err := r.m.box.Exec(ctx, box, sandbox.Cmd{Args: []string{"sh", "-c", attachScript},
			Env: map[string]string{"PROMPT": promptID, "NAME": f.Name}, Stdin: bytes.NewReader(f.Data), Stdout: out, Stderr: errs})
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", fmt.Errorf("saving %s failed (exit %d): %s", f.Name, code, clip(strings.TrimSpace(errs.String()), 1000))
		}
		dir = strings.TrimSpace(out.String())
	}
	return dir, nil
}

// withAttachments tells the agent about a prompt's files.
func withAttachments(content, dir string, files []store.Attachment) string {
	var b strings.Builder
	if content != "" {
		b.WriteString(content + "\n\n")
	}
	fmt.Fprintf(&b, "<attachments>\nFiles attached to this request, saved in %s:\n", dir)
	for _, f := range files {
		fmt.Fprintf(&b, "- %s (%s, %s)\n", f.Name, f.ContentType, byteSize(f.Size))
	}
	b.WriteString("Read the ones the request needs; reading an image or a PDF shows it to you.\n</attachments>")
	return b.String()
}

func byteSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
