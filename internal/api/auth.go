package api

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

// Signing in. A server reachable beyond this machine asks people to sign in
// with an email and password; on a new server, the first visitor creates the
// account that owns it. A signed-in browser holds a random cookie, stored
// only as its hash. Scripts and the CLI can use BEETHOVEN_TOKEN instead.

const (
	loginCookie = "beethoven_login"
	loginFor    = 30 * 24 * time.Hour
	minPassword = 8
)

// AuthState is what the UI needs to know before showing anything.
type AuthState struct {
	Required bool           `json:"required"` // the API needs a signed-in account
	Setup    bool           `json:"setup"`    // no account exists yet: the first becomes the owner
	Account  *store.Account `json:"account"`  // who is signed in
}

func (s *Server) signInRequired() bool { return s.SignIn || s.Token != "" }

// signedIn reports whether a request may use the API.
func (s *Server) signedIn(r *http.Request) bool {
	if !s.signInRequired() || s.tokenOK(r) {
		return true
	}
	_, err := s.account(r)
	return err == nil
}

func (s *Server) tokenOK(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("token") // EventSource cannot set headers
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

// account is the account a request's cookie is signed in as.
func (s *Server) account(r *http.Request) (store.Account, error) {
	c, err := r.Cookie(loginCookie)
	if err != nil || c.Value == "" {
		return store.Account{}, store.ErrNotFound
	}
	return s.M.Store().LoginAccount(r.Context(), loginID(c.Value))
}

func (s *Server) authState(w http.ResponseWriter, r *http.Request) {
	st, err := s.state(r)
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) state(r *http.Request) (AuthState, error) {
	st := AuthState{Required: s.signInRequired()}
	if a, err := s.account(r); err == nil {
		st.Account = &a
	}
	has, err := s.M.Store().HasAccounts(r.Context())
	st.Setup = st.Required && !has
	return st, err
}

type credentials struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (c *credentials) clean() {
	c.Name, c.Email = strings.TrimSpace(c.Name), strings.ToLower(strings.TrimSpace(c.Email))
}

// setup creates the owner account on a server that has none, and signs in.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	in.clean()
	switch {
	case !s.signInRequired():
		fail(w, http.StatusBadRequest, "this server doesn't ask anyone to sign in")
		return
	case !strings.Contains(in.Email, "@") || len(in.Email) > 254:
		fail(w, http.StatusBadRequest, "enter an email address")
		return
	case len(in.Password) < minPassword:
		fail(w, http.StatusBadRequest, fmt.Sprintf("use a password of at least %d characters", minPassword))
		return
	case len(in.Password) > 1024:
		fail(w, http.StatusBadRequest, "use a password of at most 1024 characters")
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		replyErr(w, err)
		return
	}
	a, err := s.M.Store().CreateOwner(r.Context(), store.Account{Email: in.Email, Name: in.Name}, hash)
	if errors.Is(err, store.ErrConflict) {
		fail(w, http.StatusConflict, "this server already has an account: sign in instead")
		return
	}
	if err != nil {
		replyErr(w, err)
		return
	}
	s.Log.Info("created the owner account", "email", a.Email)
	s.signInAs(w, r, a, http.StatusCreated)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	in.clean()
	if !s.tries.allowed(in.Email) {
		fail(w, http.StatusTooManyRequests, "too many failed attempts: try again in a few minutes")
		return
	}
	a, hash, err := s.M.Store().AccountByEmail(r.Context(), in.Email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		replyErr(w, err)
		return
	}
	if !checkPassword(hash, in.Password) { // checks a dummy hash for unknown emails, to take as long
		s.tries.failed(in.Email)
		fail(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	s.tries.reset(in.Email)
	s.signInAs(w, r, a, http.StatusOK)
}

func (s *Server) signInAs(w http.ResponseWriter, r *http.Request, a store.Account, code int) {
	b := make([]byte, 32)
	rand.Read(b)
	v := base64.RawURLEncoding.EncodeToString(b)
	exp := time.Now().Add(loginFor)
	if err := s.M.Store().CreateLogin(r.Context(), loginID(v), a.ID, exp); err != nil {
		replyErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: v, Path: "/", Expires: exp, MaxAge: int(loginFor.Seconds()),
		HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
	reply(w, code, AuthState{Required: s.signInRequired(), Account: &a})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(loginCookie); err == nil {
		if err := s.M.Store().DeleteLogin(r.Context(), loginID(c.Value)); err != nil {
			replyErr(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure(r),
		SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func loginID(cookie string) string {
	h := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(h[:])
}

// secure reports whether the browser reached the server over HTTPS, maybe
// through a proxy that terminates it.
func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// Passwords are stored as PBKDF2-SHA256 with a random salt, at OWASP's
// recommended 600,000 iterations: "pbkdf2-sha256$<iterations>$<salt>$<key>".
var passwordIterations = 600_000

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, pw, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations, enc(salt), enc(key)), nil
}

// dummyHash is checked against when there is no account, so a wrong email
// takes as long as a wrong password.
var dummyHash = sync.OnceValue(func() string { h, _ := hashPassword("not a password"); return h })

func checkPassword(hash, pw string) bool {
	known := hash != ""
	if !known {
		hash = dummyHash()
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	salt, err2 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err3 := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || err2 != nil || err3 != nil || iter < 1 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1 && known
}

// throttle limits failed sign-ins per email: maxFails within a window, then
// none until the oldest failure leaves it.
type throttle struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

const (
	maxFails   = 10
	failWindow = 15 * time.Minute
)

func (t *throttle) recent(key string) []time.Time {
	cut := time.Now().Add(-failWindow)
	l := t.fails[key]
	for len(l) > 0 && l[0].Before(cut) {
		l = l[1:]
	}
	if len(l) == 0 {
		delete(t.fails, key)
		return nil
	}
	t.fails[key] = l
	return l
}

func (t *throttle) allowed(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.recent(key)) < maxFails
}

func (t *throttle) failed(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fails == nil {
		t.fails = map[string][]time.Time{}
	}
	t.fails[key] = append(t.recent(key), time.Now())
}

func (t *throttle) reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, key)
}
