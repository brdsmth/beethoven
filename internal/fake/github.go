package fake

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// GitHub is one GitHub App on a fake GitHub: the REST API and github.com's
// OAuth endpoints, with one user. Point a client's API and Web at URL.
type GitHub struct {
	URL       string
	Key       *rsa.PrivateKey
	ClientID  string
	ExpiresIn int // lifetime of user access tokens in seconds; 0 never expire

	mu         sync.Mutex
	installs   map[string]int64 // repo → installation
	userCan    map[int64]bool   // installations the user can access
	public     map[string]bool
	userTokens map[string]bool
	tokenUser  map[string]string // user token → login; octo unless the code named another
	refresh    map[string]bool
	n          int
	Minted     []string // "installation:repository" for every installation token
	Refreshes  int
	PRAuth     []string // the Authorization of every pull request opened
	Revoked    []string
	pulls      map[string]map[string]any // "repo#number" → what GET pulls/{number} says
	checks     map[string]map[string]any // "repo@sha" → check runs and commit statuses
	PullReads  map[string]int            // "200" or "304" → count
	accounts   map[int64][2]string       // installation → the login and type of its account; octo, User by default
	perms      map[int64]map[string]string
	appPerms   map[string]string // what the app asks of installations
	Created    []map[string]any  // the body of every repository created
}

// basePerms are an installation's permissions unless SetAccount says otherwise.
var basePerms = map[string]string{"contents": "write", "pull_requests": "write", "metadata": "read"}

const fakeAppID = 4242

func NewGitHub(t testing.TB) *GitHub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	g := &GitHub{Key: key, ClientID: "Iv1.fake", installs: map[string]int64{}, userCan: map[int64]bool{},
		public: map[string]bool{}, userTokens: map[string]bool{}, tokenUser: map[string]string{}, refresh: map[string]bool{},
		pulls: map[string]map[string]any{}, checks: map[string]map[string]any{}, PullReads: map[string]int{}, accounts: map[int64][2]string{},
		perms: map[int64]map[string]string{}, appPerms: basePerms}
	srv := httptest.NewServer(g.handler(t))
	t.Cleanup(srv.Close)
	g.URL = srv.URL
	return g
}

// Install installs the app on repo as installation id; userCan says
// whether the user can access that installation.
func (g *GitHub) Install(repo string, id int64, userCan bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.installs[repo], g.userCan[id] = id, userCan
}

// SetAccount says which account installation id is on, a User or an
// Organization, and what that account granted the app.
func (g *GitHub) SetAccount(id int64, login, typ string, perms map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.accounts[id], g.perms[id], g.userCan[id] = [2]string{login, typ}, perms, true
}

// AskFor sets the permissions the app asks of the accounts that install it.
func (g *GitHub) AskFor(perms map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.appPerms = perms
}

// account returns the login, type and permissions of an installation.
func (g *GitHub) account(id int64) (string, string, map[string]string) {
	a, ok := g.accounts[id]
	if !ok {
		return "octo", "User", basePerms
	}
	return a[0], a[1], g.perms[id]
}

func (g *GitHub) SetPublic(repo string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.public[repo] = true
}

// SetPull sets what GitHub says about a pull request.
func (g *GitHub) SetPull(repo string, number int, pr map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pulls[fmt.Sprintf("%s#%d", repo, number)] = pr
}

// SetChecks sets the check runs and commit statuses on a commit, each a list
// of what GitHub's API returns for one.
func (g *GitHub) SetChecks(repo, sha string, runs, statuses []map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checks[repo+"@"+sha] = map[string]any{"runs": runs, "statuses": statuses}
}

// AppJSON is the app's credentials as the manifest conversion returns them.
func (g *GitHub) AppJSON() []byte {
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(g.Key)})
	b, _ := json.Marshal(map[string]any{"id": fakeAppID, "slug": "beethoven-test", "name": "Beethoven test",
		"client_id": g.ClientID, "client_secret": "secret", "webhook_secret": "hook", "pem": string(key),
		"html_url": g.URL + "/apps/beethoven-test", "owner": map[string]string{"login": "octo"}})
	return b
}

func (g *GitHub) handler(t testing.TB) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	asUser := func(w http.ResponseWriter, r *http.Request) bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.userTokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] {
			reply(w, 401, map[string]string{"message": "Bad credentials"})
			return false
		}
		return true
	}
	asApp := func(w http.ResponseWriter, r *http.Request) bool {
		if err := g.verifyJWT(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); err != nil {
			reply(w, 401, map[string]string{"message": err.Error()})
			return false
		}
		return true
	}

	mux.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("code") != "manifest-code" {
			reply(w, 404, map[string]string{"message": "Not Found"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write(g.AppJSON())
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		case r.Form.Get("client_id") != g.ClientID || r.Form.Get("client_secret") != "secret":
			reply(w, 200, map[string]string{"error": "incorrect_client_credentials"})
			return
		case r.Form.Get("grant_type") == "refresh_token":
			if !g.refresh[r.Form.Get("refresh_token")] {
				reply(w, 200, map[string]string{"error": "bad_refresh_token"})
				return
			}
			delete(g.refresh, r.Form.Get("refresh_token")) // single use
			g.Refreshes++
		case r.Form.Get("code") != "oauth-code" && !strings.HasPrefix(r.Form.Get("code"), "oauth-code:"):
			reply(w, 200, map[string]string{"error": "bad_verification_code"})
			return
		}
		g.n++
		access, refresh := fmt.Sprintf("ghu_%d", g.n), fmt.Sprintf("ghr_%d", g.n)
		g.userTokens[access], g.refresh[refresh] = true, true
		// "oauth-code:<login>" authorizes another user than octo.
		if _, login, ok := strings.Cut(r.Form.Get("code"), ":"); ok {
			g.tokenUser[access] = login
		}
		out := map[string]any{"access_token": access, "token_type": "bearer"}
		if g.ExpiresIn > 0 {
			out["expires_in"], out["refresh_token"], out["refresh_token_expires_in"] = g.ExpiresIn, refresh, 15897600
		}
		reply(w, 200, out)
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if !asUser(w, r) {
			return
		}
		g.mu.Lock()
		login := g.tokenUser[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		g.mu.Unlock()
		if login != "" {
			reply(w, 200, map[string]any{"id": 100 + len(login), "login": login, "name": login})
			return
		}
		reply(w, 200, map[string]any{"id": 7, "login": "octo", "name": "Octo Cat"})
	})
	mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
		if !asUser(w, r) {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		list := []map[string]any{}
		for id, ok := range g.userCan {
			if ok {
				login, typ, perms := g.account(id)
				list = append(list, map[string]any{"id": id, "account": map[string]string{"login": login, "type": typ},
					"repository_selection": "selected", "html_url": fmt.Sprintf("%s/settings/installations/%d", g.URL, id),
					"permissions": perms})
			}
		}
		reply(w, 200, map[string]any{"total_count": len(list), "installations": list})
	})
	mux.HandleFunc("GET /user/installations/{id}/repositories", func(w http.ResponseWriter, r *http.Request) {
		if !asUser(w, r) {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		repos := []map[string]any{}
		for repo, id := range g.installs {
			if fmt.Sprint(id) == r.PathValue("id") && g.userCan[id] {
				repos = append(repos, map[string]any{"full_name": repo, "private": !g.public[repo], "default_branch": "main",
					"pushed_at": "2026-10-01T00:00:00Z", "permissions": map[string]bool{"push": true}})
			}
		}
		reply(w, 200, map[string]any{"total_count": len(repos), "repositories": repos})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/installation", func(w http.ResponseWriter, r *http.Request) {
		if !asApp(w, r) {
			return
		}
		g.mu.Lock()
		id, ok := g.installs[r.PathValue("owner")+"/"+r.PathValue("name")]
		g.mu.Unlock()
		if !ok {
			reply(w, 404, map[string]string{"message": "Not Found"})
			return
		}
		reply(w, 200, map[string]any{"id": id})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		if !asApp(w, r) {
			return
		}
		var in struct{ Repositories []string }
		json.NewDecoder(r.Body).Decode(&in)
		g.mu.Lock()
		g.Minted = append(g.Minted, r.PathValue("id")+":"+strings.Join(in.Repositories, ","))
		n := len(g.Minted)
		g.mu.Unlock()
		reply(w, 201, map[string]any{"token": fmt.Sprintf("ghs_%d", n), "expires_at": "2099-01-01T00:00:00Z"})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !asUser(w, r) {
			return
		}
		repo := r.PathValue("owner") + "/" + r.PathValue("name")
		g.mu.Lock()
		_, installed := g.installs[repo]
		public := g.public[repo]
		g.mu.Unlock()
		if !installed && !public {
			reply(w, 404, map[string]string{"message": "Not Found"})
			return
		}
		reply(w, 200, map[string]any{"full_name": repo, "private": !public, "default_branch": "main"})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls/{number}", func(w http.ResponseWriter, r *http.Request) {
		if !asUser(w, r) {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		pr, ok := g.pulls[r.PathValue("owner")+"/"+r.PathValue("name")+"#"+r.PathValue("number")]
		if !ok {
			reply(w, 404, map[string]string{"message": "Not Found"})
			return
		}
		body, _ := json.Marshal(pr)
		etag := fmt.Sprintf(`"%x"`, sha256.Sum256(body))
		if r.Header.Get("If-None-Match") == etag {
			g.PullReads["304"]++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		g.PullReads["200"]++
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls", func(w http.ResponseWriter, r *http.Request) {
		if !asUser(w, r) {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		found := []any{}
		_, head, _ := strings.Cut(r.URL.Query().Get("head"), ":")
		prefix := r.PathValue("owner") + "/" + r.PathValue("name") + "#"
		for key, pr := range g.pulls {
			if ref, _ := pr["head"].(map[string]any); strings.HasPrefix(key, prefix) && ref != nil && ref["ref"] == head {
				found = append(found, map[string]any{"number": pr["number"]})
			}
		}
		reply(w, 200, found)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/commits/{sha}/check-runs", func(w http.ResponseWriter, r *http.Request) {
		if asUser(w, r) {
			g.mu.Lock()
			defer g.mu.Unlock()
			c := g.checks[r.PathValue("owner")+"/"+r.PathValue("name")+"@"+r.PathValue("sha")]
			reply(w, 200, map[string]any{"check_runs": orNone(c["runs"])})
		}
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/commits/{sha}/status", func(w http.ResponseWriter, r *http.Request) {
		if asUser(w, r) {
			g.mu.Lock()
			defer g.mu.Unlock()
			c := g.checks[r.PathValue("owner")+"/"+r.PathValue("name")+"@"+r.PathValue("sha")]
			reply(w, 200, map[string]any{"statuses": orNone(c["statuses"])})
		}
	})
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls", func(w http.ResponseWriter, r *http.Request) {
		if !asUser(w, r) {
			return
		}
		g.mu.Lock()
		g.PRAuth = append(g.PRAuth, r.Header.Get("Authorization"))
		g.mu.Unlock()
		reply(w, 201, map[string]string{"html_url": "https://github.com/" + r.PathValue("owner") + "/" + r.PathValue("name") + "/pull/1"})
	})
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, r *http.Request) {
		if asApp(w, r) {
			g.mu.Lock()
			defer g.mu.Unlock()
			reply(w, 200, map[string]any{"id": fakeAppID, "slug": "beethoven-test", "permissions": g.appPerms,
				"owner": map[string]string{"login": "octo", "type": "User"}})
		}
	})
	// Creating a repository needs an installation on its account that may
	// create repositories; the new repository joins that installation.
	create := func(w http.ResponseWriter, r *http.Request, owner string) {
		if !asUser(w, r) {
			return
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		g.mu.Lock()
		defer g.mu.Unlock()
		var inst int64
		for id, ok := range g.userCan {
			login, _, perms := g.account(id)
			if ok && login == owner && (perms["repository_creation"] == "write" || perms["administration"] == "write") {
				inst = id
			}
		}
		repo := owner + "/" + fmt.Sprint(in["name"])
		if _, exists := g.installs[repo]; exists {
			reply(w, 422, map[string]any{"message": "Repository creation failed.",
				"errors": []map[string]string{{"message": "name already exists on this account"}}})
			return
		}
		if inst == 0 {
			reply(w, 403, map[string]string{"message": "Resource not accessible by integration"})
			return
		}
		g.installs[repo] = inst
		g.public[repo] = in["private"] == false
		g.Created = append(g.Created, in)
		reply(w, 201, map[string]any{"full_name": repo, "private": in["private"], "default_branch": "main",
			"permissions": map[string]bool{"push": true}})
	}
	mux.HandleFunc("POST /user/repos", func(w http.ResponseWriter, r *http.Request) { create(w, r, "octo") })
	mux.HandleFunc("POST /orgs/{org}/repos", func(w http.ResponseWriter, r *http.Request) { create(w, r, r.PathValue("org")) })
	mux.HandleFunc("DELETE /applications/{client}/grant", func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		var in struct {
			AccessToken string `json:"access_token"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if id != g.ClientID || secret != "secret" {
			reply(w, 401, map[string]string{"message": "Bad credentials"})
			return
		}
		g.mu.Lock()
		g.Revoked = append(g.Revoked, in.AccessToken)
		delete(g.userTokens, in.AccessToken)
		g.mu.Unlock()
		w.WriteHeader(204)
	})
	return mux
}

// verifyJWT checks a token the app signed for itself.
func (g *GitHub) verifyJWT(tok string) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return fmt.Errorf("not a JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&g.Key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("bad signature")
	}
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct{ Iss string }
	json.Unmarshal(claims, &c)
	if c.Iss != g.ClientID {
		return fmt.Errorf("iss = %q", c.Iss)
	}
	return nil
}

func orNone(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}
