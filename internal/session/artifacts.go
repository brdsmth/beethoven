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

// MaxArtifactBytes bounds a file shown in the viewer.
const MaxArtifactBytes = 25 << 20

// ShowFile copies a file out of the session's sandbox and announces it, so
// the viewer opens it. A relative path is from the agent's working
// directory. Showing the same path again adds a version.
func (m *Manager) ShowFile(ctx context.Context, sessionID, file, title string) (store.Artifact, error) {
	r, err := m.runner(ctx, sessionID)
	if err != nil {
		return store.Artifact{}, err
	}
	return r.showFile(ctx, file, title)
}

// Artifacts lists the files shown in a session.
func (m *Manager) Artifacts(ctx context.Context, sessionID string) ([]store.Artifact, error) {
	return m.db.Artifacts(ctx, sessionID)
}

// readFile prints the file, refusing directories and files over the limit
// before reading them. Exit 3: missing; 4: too large.
const readFile = `[ -f "$FILE" ] || { echo "no such file: $FILE" >&2; exit 3; }
size=$(wc -c < "$FILE")
[ "$size" -le "$MAX" ] || { echo "$FILE is $size bytes; the viewer takes up to $MAX" >&2; exit 4; }
cat "$FILE"`

func (r *runner) showFile(ctx context.Context, file, title string) (store.Artifact, error) {
	file = strings.TrimSpace(file)
	if file == "" {
		return store.Artifact{}, fmt.Errorf("%w: say which file", ErrInvalid)
	}
	s := r.snapshot()
	if !path.IsAbs(file) {
		file = path.Join(Workdir(s), file)
	}
	file = path.Clean(file)
	id, release, err := r.acquire(ctx)
	if err != nil {
		return store.Artifact{}, err
	}
	defer release()
	var out bytes.Buffer
	errs := &tail{max: 2 << 10}
	code, err := r.m.box.Exec(ctx, id, sandbox.Cmd{Args: []string{"sh", "-c", readFile},
		Env: map[string]string{"FILE": file, "MAX": fmt.Sprint(MaxArtifactBytes)}, Stdout: &out, Stderr: errs})
	if err != nil {
		return store.Artifact{}, err
	}
	if code != 0 {
		return store.Artifact{}, fmt.Errorf("%w: %s", ErrInvalid, strings.TrimSpace(errs.String()))
	}
	if out.Len() > MaxArtifactBytes { // it grew while being read
		return store.Artifact{}, fmt.Errorf("%w: %s is over %d bytes", ErrInvalid, file, MaxArtifactBytes)
	}
	a := store.Artifact{SessionID: s.ID, PromptID: r.currentPrompt(), Path: file, Name: path.Base(file),
		Title: strings.TrimSpace(title), ContentType: ContentType(file, out.Bytes()), Data: out.Bytes()}
	if err := r.m.db.CreateArtifact(ctx, &a); err != nil {
		return a, err
	}
	r.emit(a.PromptID, EvArtifact, map[string]any{"kind": "file", "id": a.ID, "path": a.Path, "name": a.Name,
		"title": a.Title, "content_type": a.ContentType, "size": a.Size})
	return a, nil
}

// textTypes are extensions that are text whatever the system's MIME table
// says, so the viewer shows them rather than offering a download.
var textTypes = map[string]string{
	".md": "text/markdown", ".markdown": "text/markdown", ".csv": "text/csv", ".json": "application/json",
	".txt": "text/plain", ".log": "text/plain", ".yaml": "text/plain", ".yml": "text/plain", ".toml": "text/plain",
	".go": "text/plain", ".ts": "text/plain", ".tsx": "text/plain", ".js": "text/plain", ".jsx": "text/plain",
	".py": "text/plain", ".rs": "text/plain", ".sh": "text/plain", ".sql": "text/plain", ".css": "text/plain",
	".swift": "text/plain", ".kt": "text/plain", ".java": "text/plain", ".rb": "text/plain", ".c": "text/plain",
	".h": "text/plain", ".diff": "text/plain", ".patch": "text/plain", ".xml": "text/plain",
	".html": "text/html", ".htm": "text/html", ".svg": "image/svg+xml",
}

// ContentType names a file's type from its extension, else its content.
func ContentType(name string, data []byte) string {
	ext := strings.ToLower(path.Ext(name))
	t := textTypes[ext]
	if t == "" {
		t = mime.TypeByExtension(ext)
	}
	if t == "" {
		t = http.DetectContentType(data)
		if strings.HasPrefix(t, "application/octet-stream") && utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
			t = "text/plain" // an unknown extension holding text
		}
	}
	if base, _, _ := mime.ParseMediaType(t); base != "" {
		t = base
	}
	if strings.HasPrefix(t, "text/") || t == "application/json" {
		t += "; charset=utf-8"
	}
	return t
}
