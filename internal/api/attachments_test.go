package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/session"
)

type upload struct {
	name, contentType string
	data              []byte
}

// postForm sends request as JSON with files attached, as the web UI does.
func postForm(t *testing.T, url string, request any, files ...upload) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if request != nil {
		j, _ := json.Marshal(request)
		w.WriteField("request", string(j))
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="file"; filename="`+f.name+`"`)
		h.Set("Content-Type", f.contentType)
		part, _ := w.CreatePart(h)
		part.Write(f.data)
	}
	w.Close()
	resp, err := http.Post(url, w.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestAttachments(t *testing.T) {
	srv, _ := server(t, "")
	png := []byte("\x89PNG\r\n\x1a\nimage")
	code, out := postForm(t, srv.URL+"/api/sessions", map[string]any{"prompt": "what's wrong here?"},
		upload{"shot.png", "image/png", png}, upload{"page.html", "text/plain", []byte("<html><script>alert(1)</script>")})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	sessionID := out["session"].(map[string]any)["id"].(string)
	files := out["prompt"].(map[string]any)["attachments"].([]any)
	if len(files) != 2 {
		t.Fatalf("attachments = %v", files)
	}
	shot, page := files[0].(map[string]any), files[1].(map[string]any)
	if shot["name"] != "shot.png" || shot["content_type"] != "image/png" || shot["size"] != float64(len(png)) || shot["data"] != nil {
		t.Errorf("shot = %v", shot)
	}

	get := func(id string) (*http.Response, []byte) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/sessions/" + sessionID + "/attachments/" + id)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}
	resp, body := get(shot["id"].(string))
	if resp.StatusCode != 200 || !bytes.Equal(body, png) || resp.Header.Get("Content-Type") != "image/png" ||
		resp.Header.Get("Content-Disposition") != `inline; filename=shot.png` ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("image: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	// HTML is never shown from this origin, whatever its uploader declared.
	resp, _ = get(page["id"].(string))
	if ct, cd := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"); !strings.HasPrefix(ct, "text/html") ||
		!strings.HasPrefix(cd, "attachment;") {
		t.Errorf("HTML served as %q, %q; want a text/html download", ct, cd)
	}
	if resp, _ := http.Get(srv.URL + "/api/sessions/nope/attachments/" + shot["id"].(string)); resp.StatusCode != 404 {
		t.Errorf("through another session: %d, want 404", resp.StatusCode)
	}

	// A follow-up with a file and no text.
	code, out = postForm(t, srv.URL+"/api/sessions/"+sessionID+"/prompts", map[string]any{"content": ""},
		upload{"résumé.txt", "", []byte("notes")})
	if code != http.StatusCreated {
		t.Fatalf("follow-up: %d %v", code, out)
	}
	a := out["attachments"].([]any)[0].(map[string]any)
	resp, _ = get(a["id"].(string))
	if cd := resp.Header.Get("Content-Disposition"); cd != `inline; filename*=utf-8''r%C3%A9sum%C3%A9.txt` {
		t.Errorf("non-ASCII name: %q", cd)
	}
}

func TestAttachmentRequestsRefused(t *testing.T) {
	srv, _ := server(t, "")
	url := srv.URL + "/api/sessions"
	var many []upload
	for range session.MaxAttachments + 1 {
		many = append(many, upload{"f.txt", "text/plain", []byte("x")})
	}
	for _, c := range []struct {
		what    string
		request any
		files   []upload
		want    int
	}{
		{"no request field", nil, []upload{{"f.txt", "text/plain", []byte("x")}}, http.StatusBadRequest},
		{"an unknown field", map[string]any{"prompt": "p", "bogus": 1}, nil, http.StatusBadRequest},
		{"too many files", map[string]any{"prompt": "p"}, many, http.StatusRequestEntityTooLarge},
		{"too much data", map[string]any{"prompt": "p"}, []upload{{"a", "", []byte("x")},
			{"b", "", make([]byte, session.MaxAttachmentBytes)}}, http.StatusRequestEntityTooLarge},
		{"nothing at all", map[string]any{"prompt": " "}, nil, http.StatusBadRequest},
	} {
		if code, out := postForm(t, url, c.request, c.files...); code != c.want {
			t.Errorf("%s: %d %v, want %d", c.what, code, out, c.want)
		}
	}
}
