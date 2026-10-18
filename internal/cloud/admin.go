package cloud

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

// The operator's pages, at app.<domain>/admin, behind HTTP basic auth with
// the admin token as the password: every workspace with its status and
// migrations, and the actions on them. Forms from other sites are refused.

func (s *Service) admin(w http.ResponseWriter, r *http.Request) {
	_, pass, ok := r.BasicAuth()
	if s.cfg.AdminToken == "" || !ok || !equal(pass, s.cfg.AdminToken) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Beethoven admin", charset="UTF-8"`)
		http.Error(w, "the admin token is required", http.StatusUnauthorized)
		return
	}
	http.NewCrossOriginProtection().Handler(http.HandlerFunc(s.adminRoutes)).ServeHTTP(w, r)
}

func (s *Service) adminRoutes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := strings.TrimPrefix(r.URL.Path, "/admin")
	if r.Method == http.MethodGet {
		switch path {
		case "", "/":
			s.adminPage(w, r, "", "")
		case "/workspaces.json":
			list, err := s.reg.list(ctx)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(orEmpty(list))
		default:
			http.NotFound(w, r)
		}
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	if path == "/workspaces" {
		ws, link, err := s.Create(ctx, r.Form.Get("slug"), r.Form.Get("name"))
		if err != nil {
			s.adminPage(w, r, "", errText(err))
			return
		}
		go s.Retry(s.base(), ws.ID) // open it now: its database is created and migrated
		s.adminPage(w, r, "Created "+ws.Slug+". Send its owner this link to claim it; it works once, for 7 days: "+link, "")
		return
	}
	id, action, _ := strings.Cut(strings.TrimPrefix(path, "/workspaces/"), "/")
	var msg string
	var err error
	switch action {
	case "retry":
		err = s.Retry(ctx, id)
		msg = "Opened it again."
	case "suspend":
		err = s.SetStatus(ctx, id, StatusSuspended)
		msg = "Suspended it."
	case "activate":
		err = s.SetStatus(ctx, id, StatusActive)
		msg = "Reactivated it."
	case "reclaim":
		var link string
		link, err = s.Reclaim(ctx, id)
		msg = "A new claim link, which works once, for 7 days: " + link
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.adminPage(w, r, "", errText(err))
		return
	}
	s.adminPage(w, r, msg, "")
}

func (s *Service) adminPage(w http.ResponseWriter, r *http.Request, note, problem string) {
	list, err := s.reg.list(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	adminTmpl.Execute(w, map[string]any{"Workspaces": list, "Note": note, "Problem": problem, "Domain": s.cfg.Domain, "URL": s.URL})
}

func errText(err error) string {
	if errors.Is(err, store.ErrInvalid) {
		return strings.TrimPrefix(err.Error(), store.ErrInvalid.Error()+": ")
	}
	return err.Error()
}

func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

var adminTmpl = template.Must(template.New("admin").Funcs(template.FuncMap{
	"when": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.UTC().Format("Jan 2 15:04 UTC")
	},
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Admin · Beethoven Cloud</title>
<style>
:root { --bg:#fcfcfd; --text:#1c1c21; --muted:#62626e; --border:#e3e3e8; --accent:#3e63dd; --red:#cf3434; --green:#18864b; --amber:#a15c07; --panel:#fff }
@media (prefers-color-scheme: dark) { :root { --bg:#111113; --text:#e8e8ec; --muted:#a0a0ab; --border:#2b2b31; --accent:#5b7cf0; --red:#f2696a; --green:#4cc38a; --amber:#e5a43a; --panel:#1c1c20 } }
body { margin:0; padding:24px 16px; background:var(--bg); color:var(--text); font:14px/1.5 system-ui, sans-serif }
main { max-width:1100px; margin:0 auto }
h1 { font-size:18px; margin:0 0 16px }
.note, .problem { padding:8px 12px; border-radius:6px; margin:0 0 16px; overflow-wrap:anywhere }
.note { background:color-mix(in srgb, var(--green) 14%, transparent) }
.problem { background:color-mix(in srgb, var(--red) 14%, transparent) }
.scroll { overflow-x:auto }
table { width:100%; border-collapse:collapse; background:var(--panel); border:1px solid var(--border); border-radius:6px }
th, td { text-align:left; padding:8px 10px; border-top:1px solid var(--border); vertical-align:top; font-size:13px }
th { color:var(--muted); font-weight:500; border-top:0 }
.err { color:var(--red); max-width:320px; overflow-wrap:anywhere }
.ok { color:var(--green) } .muted { color:var(--muted) } .warn { color:var(--amber) }
form.inline { display:inline }
button { font:inherit; padding:3px 8px; border:1px solid var(--border); border-radius:5px; background:var(--panel); color:var(--text); cursor:pointer }
.new { display:flex; flex-wrap:wrap; gap:8px; align-items:center; margin:20px 0 }
.new input { font:inherit; padding:5px 8px; border:1px solid var(--border); border-radius:5px; background:var(--panel); color:var(--text) }
code { font-size:12px }
</style></head><body><main>
<h1>Beethoven Cloud · workspaces</h1>
{{if .Note}}<p class="note">{{.Note}}</p>{{end}}
{{if .Problem}}<p class="problem">{{.Problem}}</p>{{end}}
<div class="scroll"><table>
<tr><th>Workspace</th><th>Status</th><th>Plan</th><th>Database</th><th>Migrated</th><th></th></tr>
{{range .Workspaces}}<tr>
<td><a href="{{call $.URL .Slug}}/">{{.Slug}}</a><div class="muted">{{.Name}}</div>{{if not .Claimed}}<div class="warn">not claimed yet</div>{{end}}</td>
<td>{{if eq .Status "active"}}<span class="ok">active</span>{{else}}<span class="warn">{{.Status}}</span>{{end}}</td>
<td>{{.Plan}}</td>
<td>{{.Migrations}} migrations{{if .LastMigration}}<div class="muted"><code>{{.LastMigration}}</code></div>{{end}}
{{if .MigrationError}}<div class="err">Failed to open: {{.MigrationError}}</div>{{end}}</td>
<td>{{when .MigratedAt}}</td>
<td>
<form class="inline" method="post" action="/admin/workspaces/{{.ID}}/retry"><button>Reopen</button></form>
{{if eq .Status "active"}}<form class="inline" method="post" action="/admin/workspaces/{{.ID}}/suspend"><button>Suspend</button></form>
{{else}}<form class="inline" method="post" action="/admin/workspaces/{{.ID}}/activate"><button>Reactivate</button></form>{{end}}
{{if not .Claimed}}<form class="inline" method="post" action="/admin/workspaces/{{.ID}}/reclaim"><button>New claim link</button></form>{{end}}
</td></tr>
{{else}}<tr><td colspan="6" class="muted">No workspaces yet.</td></tr>{{end}}
</table></div>
<form class="new" method="post" action="/admin/workspaces">
<strong>New workspace</strong>
<input name="slug" placeholder="address, e.g. acme" required pattern="[a-z0-9-]{3,30}"> <span class="muted">.{{.Domain}}</span>
<input name="name" placeholder="name, e.g. Acme" required maxlength="80">
<button>Create</button>
</form>
</main></body></html>`))

// page is a small page for people who land on the service's own addresses.
func page(w http.ResponseWriter, code int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	pageTmpl.Execute(w, map[string]string{"Title": title, "Text": text})
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root { --bg:#fcfcfd; --text:#1c1c21; --muted:#62626e }
@media (prefers-color-scheme: dark) { :root { --bg:#111113; --text:#e8e8ec; --muted:#a0a0ab } }
body { margin:0; min-height:100vh; display:grid; place-items:center; background:var(--bg); color:var(--text); font:15px/1.5 system-ui, sans-serif; padding:16px; box-sizing:border-box }
main { max-width:420px } h1 { font-size:20px; margin:0 0 8px } p { color:var(--muted); margin:0 }
</style></head><body><main><h1>{{.Title}}</h1><p>{{.Text}}</p></main></body></html>`))
