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
  BEETHOVEN_ADDR          listen address (127.0.0.1:7070)
  BEETHOVEN_DATA          data directory (~/.beethoven)
  BEETHOVEN_TOKEN         require this token on the API
  BEETHOVEN_HOSTS         extra Host names to accept, comma separated
  BEETHOVEN_IMAGE         sandbox image (beethoven-sandbox:latest)
  BEETHOVEN_SANDBOX_URL   server URL as seen from sandboxes (http://host.docker.internal:<port>)
  BEETHOVEN_IDLE          stop idle sandboxes after (10m)
  BEETHOVEN_TURN_TIMEOUT  limit for one prompt (2h)
  BEETHOVEN_MODEL         initial default model (then: Settings)
  GITHUB_TOKEN            GitHub credential (default: gh auth token)
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
	addr := fs.String("addr", env("BEETHOVEN_ADDR", "127.0.0.1:7070"), "listen address")
	dataDir := fs.String("data", env("BEETHOVEN_DATA", filepath.Join(home(), ".beethoven")), "data directory")
	fs.Parse(args)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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

	image := env("BEETHOVEN_IMAGE", "beethoven-sandbox:latest")
	docker := &sandbox.Docker{}
	if image == "beethoven-sandbox:latest" {
		log.Info("checking sandbox image", "image", image)
		if err := docker.EnsureImage(ctx, image, sandbox.Dockerfile, os.Stderr); err != nil {
			return err
		}
	}

	_, port, err := net.SplitHostPort(*addr)
	if err != nil {
		return fmt.Errorf("addr: %w", err)
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
	gh.Stored = func(ctx context.Context) string {
		creds, _ := db.Credentials(ctx)
		return creds["GITHUB_TOKEN"]
	}
	ghToken, _ := gh.Token(ctx)

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
		ToolsURL:    env("BEETHOVEN_SANDBOX_URL", "http://host.docker.internal:"+port) + "/mcp",
		TokenKey:    tokenKey[:],
		Committer:   store.Author{Name: "Beethoven", Email: "beethoven@localhost"},
		Env:         passEnv,
	}, db, docker, agent.Claude{}, gh, log)
	auto := automation.New(m, log)
	if err := m.Start(ctx); err != nil {
		return err
	}
	go auto.Run(ctx)

	if model := os.Getenv("BEETHOVEN_MODEL"); model != "" {
		if s, err := db.Settings(ctx); err == nil && s.Model == "" { // seed the setting from the environment once
			s.Model = model
			db.SaveSettings(ctx, s)
		}
	}
	srv := &http.Server{Addr: *addr, Handler: (&api.Server{
		M: m, Auto: auto, GitHub: gh, Token: os.Getenv("BEETHOVEN_TOKEN"), Hosts: splitList(os.Getenv("BEETHOVEN_HOSTS")),
		UI: web.FS(), Log: log, Version: version,
	}).Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("beethoven is running", "url", "http://"+*addr, "data", *dataDir, "github", ghToken != "")
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
