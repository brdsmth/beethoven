// Command beethoven runs background coding agents: `beethoven serve` starts
// the server; the other subcommands are a terminal client for it.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/api"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/slack"
	"github.com/brdsmth/beethoven/internal/store"
	"github.com/brdsmth/beethoven/web"
)

const version = "0.1.0"

const usage = `beethoven — background coding agents

Server:
  beethoven serve                          start the server (UI at http://127.0.0.1:7070)

Client (talks to $BEETHOVEN_URL, default http://127.0.0.1:7070):
  beethoven run [flags] <prompt>           start a session and follow it
      -env name | -repo owner/name[@branch] (repeatable)
      -model m  -effort e  -title t  -d (don't follow)
  beethoven send [-d] <session> <prompt>   queue a follow-up prompt
  beethoven tail <session>                 follow a session's timeline
  beethoven ls [-a]                        list sessions (-a: archived)
  beethoven stop <session>                 stop the running prompt, cancel queued ones
  beethoven env ls                         list environments
  beethoven secret set [-scope s] NAME     store a secret (value read from stdin)
  beethoven secret import [-scope s]       store every KEY=value of a .env file on stdin
  beethoven secret ls | rm [-scope s] NAME
      scope: empty (global), owner/name, or env:<environment name>

Server environment:
  BEETHOVEN_ADDR          listen address (127.0.0.1:7070, or 0.0.0.0:$PORT when PORT is set)
  BEETHOVEN_URL           the server's public HTTPS address (on Railway: its domain)
  BEETHOVEN_DATA          data directory (~/.beethoven)
  BEETHOVEN_TOKEN         require this token on the API (required beyond localhost)
  BEETHOVEN_HOSTS         extra Host names to accept, comma separated
  BEETHOVEN_SANDBOX       sandbox provider: docker, or modal (default when MODAL_TOKEN_ID is set)
  MODAL_TOKEN_ID, MODAL_TOKEN_SECRET
                          Modal credentials (or ~/.modal.toml)
  BEETHOVEN_DOCKER_NETWORK
                          Docker network for sandboxes, e.g. one the server shares
  BEETHOVEN_IMAGE         sandbox image (beethoven-sandbox:latest)
  BEETHOVEN_SANDBOX_URL   server URL as seen from sandboxes (Docker: http://host.docker.internal:<port>;
                          Modal: BEETHOVEN_URL)
  BEETHOVEN_IDLE          stop idle sandboxes after (10m)
  BEETHOVEN_TURN_TIMEOUT  limit for one prompt (2h)
  BEETHOVEN_MODEL         initial default model (then: Settings)
  BEETHOVEN_GITHUB_APP    a preconfigured GitHub App: credentials JSON, or a file holding it
  BEETHOVEN_SLACK_APP     a preconfigured Slack app: credentials JSON, or a file holding it
  GITHUB_TOKEN            GitHub token until someone connects the app (default: gh auth token)
  ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN
                          passed to sandboxes unless overridden by secrets
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "serve":
		err = serve(args)
	case "run":
		err = cmdRun(args)
	case "send":
		err = cmdSend(args)
	case "tail":
		err = cmdTail(args)
	case "ls":
		err = cmdList(args)
	case "stop":
		err = cmdStop(args)
	case "secret":
		err = cmdSecret(args)
	case "env":
		err = cmdEnv(args)
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "beethoven:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", env("BEETHOVEN_ADDR", defaultAddr()), "listen address")
	dataDir := fs.String("data", env("BEETHOVEN_DATA", filepath.Join(home(), ".beethoven")), "data directory")
	fs.Parse(args)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := needToken(*addr, os.Getenv("BEETHOVEN_TOKEN")); err != nil {
		return err
	}
	if os.Getenv("RAILWAY_ENVIRONMENT") != "" && os.Getenv("RAILWAY_VOLUME_MOUNT_PATH") == "" {
		log.Warn("no Railway volume is attached: sessions, settings and secrets will be lost on the next deploy; " +
			"attach one at /data (railway volume add --mount-path /data)")
	}
	public := publicURL()

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return err
	}
	key, err := secretKey(*dataDir)
	if err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(*dataDir, "beethoven.db"), key)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	image := env("BEETHOVEN_IMAGE", sandbox.DefaultImage)
	_, port, err := net.SplitHostPort(*addr)
	if err != nil {
		return fmt.Errorf("addr: %w", err)
	}
	box, toolsBase, err := sandboxes(ctx, log, image, port, public)
	if err != nil {
		return err
	}
	idle, err := time.ParseDuration(env("BEETHOVEN_IDLE", "10m"))
	if err != nil {
		return fmt.Errorf("BEETHOVEN_IDLE: %w", err)
	}
	turnTimeout, err := time.ParseDuration(env("BEETHOVEN_TURN_TIMEOUT", "2h"))
	if err != nil {
		return fmt.Errorf("BEETHOVEN_TURN_TIMEOUT: %w", err)
	}
	author := store.Author{Name: env("BEETHOVEN_GIT_NAME", gitConfig("user.name")), Email: env("BEETHOVEN_GIT_EMAIL", gitConfig("user.email"))}
	gh := github.New(os.Getenv("GITHUB_TOKEN"))
	gh.DB = db
	if v := os.Getenv("BEETHOVEN_GITHUB_APP"); v != "" {
		if gh.EnvApp, err = github.LoadApp(v); err != nil {
			return fmt.Errorf("BEETHOVEN_GITHUB_APP: %w", err)
		}
	}

	passEnv := map[string]string{}
	for _, k := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		if v := os.Getenv(k); v != "" {
			passEnv[k] = v
		}
	}
	tokenKey := sha256.Sum256(append([]byte("beethoven tool tokens:"), key...))
	m := session.New(session.Config{
		Image:       image,
		IdleTimeout: idle,
		TurnTimeout: turnTimeout,
		Author:      author,
		ToolsURL:    toolsBase + "/mcp",
		TokenKey:    tokenKey[:],
		Committer:   store.Author{Name: "Beethoven", Email: "beethoven@localhost"},
		Env:         passEnv,
	}, db, box, agent.Claude{}, gh, log)
	auto := automation.New(m, log)
	sl := &slack.Client{DB: db}
	if v := os.Getenv("BEETHOVEN_SLACK_APP"); v != "" {
		if sl.EnvApp, err = slack.LoadApp(v); err != nil {
			return fmt.Errorf("BEETHOVEN_SLACK_APP: %w", err)
		}
	}
	bot := slack.NewBot(sl, m, func(ctx context.Context) ([]string, error) {
		if gh.Mode(ctx) == "" {
			return nil, nil
		}
		repos, err := gh.Repos(ctx)
		names := make([]string, len(repos))
		for i, r := range repos {
			names[i] = r.Repo
		}
		return names, err
	}, log)
	if err := m.Start(ctx); err != nil {
		return err
	}
	go auto.Run(ctx)
	go gh.TrackPullRequests(ctx, 10*time.Minute, log)

	if model := os.Getenv("BEETHOVEN_MODEL"); model != "" {
		if s, err := db.Settings(ctx); err == nil && s.Model == "" { // seed the setting from the environment once
			s.Model = model
			db.SaveSettings(ctx, s)
		}
	}
	hosts := splitList(os.Getenv("BEETHOVEN_HOSTS"))
	if u, err := url.Parse(public); err == nil && u.Hostname() != "" {
		hosts = append(hosts, u.Hostname())
	}
	srv := &http.Server{Addr: *addr, Handler: (&api.Server{
		M: m, Auto: auto, GitHub: gh, Slack: sl, SlackBot: bot, Token: os.Getenv("BEETHOVEN_TOKEN"), Hosts: hosts,
		UI: web.FS(), Log: log, Version: version,
	}).Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("beethoven is running", "url", "http://"+*addr, "data", *dataDir, "github", gh.Mode(ctx))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	m.Shutdown(15 * time.Second)
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// secretKey loads the key that encrypts secrets and signs tool tokens,
// creating it on first run.
func secretKey(dir string) ([]byte, error) {
	if v := os.Getenv("BEETHOVEN_SECRET_KEY"); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(k) != 32 {
			return nil, errors.New("BEETHOVEN_SECRET_KEY must be 32 bytes, base64 encoded")
		}
		return k, nil
	}
	path := filepath.Join(dir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		return base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	}
	k := make([]byte, 32)
	rand.Read(k)
	return k, os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(k)+"\n"), 0o600)
}

// sandboxes opens the sandbox provider, and says where sandboxes reach this
// server: Modal when BEETHOVEN_SANDBOX says so or Modal credentials are set,
// otherwise the host's Docker.
func sandboxes(ctx context.Context, log *slog.Logger, image, port, public string) (sandbox.Provider, string, error) {
	kind := os.Getenv("BEETHOVEN_SANDBOX")
	if kind == "" {
		kind = "docker"
		if os.Getenv("MODAL_TOKEN_ID") != "" {
			kind = "modal"
		}
	}
	switch kind {
	case "docker":
		if exec.CommandContext(ctx, "docker", "info").Run() != nil {
			log.Warn("no sandboxes: Docker isn't available here; set MODAL_TOKEN_ID and MODAL_TOKEN_SECRET to use Modal")
			return sandbox.Unavailable{Reason: "no sandbox provider is configured: set MODAL_TOKEN_ID and MODAL_TOKEN_SECRET " +
				"to run sandboxes on Modal, or run Beethoven where Docker is available"}, "", nil
		}
		d := &sandbox.Docker{}
		if network := os.Getenv("BEETHOVEN_DOCKER_NETWORK"); network != "" {
			d.Args = []string{"--network", network}
		}
		if image == sandbox.DefaultImage {
			log.Info("checking sandbox image", "image", image)
			if err := d.EnsureImage(ctx, image, sandbox.Dockerfile, os.Stderr); err != nil {
				return nil, "", err
			}
		}
		return d, env("BEETHOVEN_SANDBOX_URL", "http://host.docker.internal:"+port), nil
	case "modal":
		url := env("BEETHOVEN_SANDBOX_URL", public)
		if url == "" {
			return nil, "", errors.New("Modal sandboxes reach this server over the internet: set BEETHOVEN_URL to its public HTTPS address")
		}
		m, err := sandbox.NewModal(env("BEETHOVEN_MODAL_APP", "beethoven"))
		if err != nil {
			return nil, "", fmt.Errorf("modal: %w", err)
		}
		go func() {
			log.Info("modal: building the sandbox image (the first time takes a few minutes)")
			if err := m.Warm(ctx); err != nil && ctx.Err() == nil {
				log.Warn("modal: building the sandbox image", "err", err)
				return
			}
			log.Info("modal: sandbox image ready")
		}()
		return m, url, nil
	}
	return nil, "", fmt.Errorf("BEETHOVEN_SANDBOX: unknown provider %q; use docker or modal", kind)
}

// defaultAddr listens on localhost, or on the port a platform such as
// Railway assigns in $PORT.
func defaultAddr() string {
	if port := os.Getenv("PORT"); port != "" {
		return "0.0.0.0:" + port
	}
	return "127.0.0.1:7070"
}

// publicURL is the server's address on the internet, if it has one: the
// BEETHOVEN_URL setting, or the domain Railway assigns.
func publicURL() string {
	if v := strings.TrimSuffix(os.Getenv("BEETHOVEN_URL"), "/"); v != "" {
		return v
	}
	if d := os.Getenv("RAILWAY_PUBLIC_DOMAIN"); d != "" {
		return "https://" + d
	}
	return ""
}

// needToken refuses to serve beyond localhost without an API token: anyone
// who could reach the server could run agents with its credentials.
func needToken(addr, token string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("addr: %w", err)
	}
	if ip := net.ParseIP(host); token == "" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("set BEETHOVEN_TOKEN to listen on %s: without it, anyone who can reach the server can run agents with its credentials", addr)
	}
	return nil
}

func gitConfig(key string) string {
	out, _ := exec.Command("git", "config", key).Output()
	return strings.TrimSpace(string(out))
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
