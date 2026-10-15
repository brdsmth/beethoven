package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// decodeWithFiles reads a JSON request, like decode, or a multipart form
// that carries the JSON in its "request" field and files to attach in
// "file" fields.
func decodeWithFiles(w http.ResponseWriter, r *http.Request, v any) ([]store.Attachment, bool) {
	if t, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); t != "multipart/form-data" {
		return nil, decode(w, r, v)
	}
	r.Body = http.MaxBytesReader(w, r.Body, session.MaxAttachmentBytes+2<<20)
	form, err := r.MultipartReader()
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid form: "+err.Error())
		return nil, false
	}
	var files []store.Attachment
	var total int64
	decoded := false
	tooLarge := func() ([]store.Attachment, bool) {
		fail(w, http.StatusRequestEntityTooLarge, session.AttachmentLimits)
		return nil, false
	}
	for {
		part, err := form.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.As(err, new(*http.MaxBytesError)) {
				return tooLarge()
			}
			fail(w, http.StatusBadRequest, "invalid form: "+err.Error())
			return nil, false
		}
		switch part.FormName() {
		case "request":
			dec := json.NewDecoder(io.LimitReader(part, 1<<20))
			dec.DisallowUnknownFields()
			if err := dec.Decode(v); err != nil {
				fail(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
				return nil, false
			}
			decoded = true
		case "file":
			if len(files) == session.MaxAttachments {
				return tooLarge()
			}
			data, err := io.ReadAll(io.LimitReader(part, session.MaxAttachmentBytes-total+1))
			if total += int64(len(data)); total > session.MaxAttachmentBytes || errors.As(err, new(*http.MaxBytesError)) {
				return tooLarge()
			}
			if err != nil {
				fail(w, http.StatusBadRequest, "invalid form: "+err.Error())
				return nil, false
			}
			files = append(files, store.Attachment{Name: part.FileName(), ContentType: part.Header.Get("Content-Type"), Data: data})
		}
	}
	if !decoded {
		fail(w, http.StatusBadRequest, `the form has no "request" field`)
		return nil, false
	}
	return files, true
}

// shownInline are the types a browser may show in place. Everything else
// is downloaded: an HTML or SVG file shown from this origin could act as
// the person viewing it.
var shownInline = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "text/plain": true}

// attachment serves an attached file.
func (s *Server) attachment(w http.ResponseWriter, r *http.Request) {
	a, err := s.M.Store().Attachment(r.Context(), r.PathValue("id"), r.PathValue("aid"))
	if err != nil {
		replyErr(w, err)
		return
	}
	disposition := "attachment"
	if t, _, _ := mime.ParseMediaType(a.ContentType); shownInline[t] {
		disposition = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", a.ContentType)
	h.Set("Content-Disposition", firstOf(mime.FormatMediaType(disposition, map[string]string{"filename": a.Name}), disposition))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox") // no scripts, should a browser render it anyway
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(a.Data))
}
