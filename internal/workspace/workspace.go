// Package workspace runs one workspace: its database, sessions,
// automations, GitHub and Slack, and the HTTP API that serves them. A
// self-hosted server is one workspace; the cloud runs many in one process,
// each with its own database and key.
package workspace

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/brdsmth/beethoven/internal/agent"
	"github.com/brdsmth/beethoven/internal/api"
	"github.com/brdsmth/beethoven/internal/automation"
	"github.com/brdsmth/beethoven/internal/github"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/slack"
	"github.com/brdsmth/beethoven/internal/store"
)

// Config is what a workspace runs with.
type Config struct {
	Dir string // holds beethoven.db
	Key []byte // 32 bytes: encrypts its secrets and signs its agents' tool tokens

	Box           sandbox.Provider
	Image         string
	SandboxPrefix string // names its sandboxes; "" = "beethoven-"
	IdleTimeout   time.Duration
	TurnTimeout   time.Duration
	ToolsURL      string // its MCP endpoint, as sandboxes reach it
	Author        store.Author
	Env           map[string]string // passed to every sandbox, under secrets
	Model         string            // the default model, set once if none is

	GitHubToken string      // a personal access token; "" asks the gh CLI
	NoToken     bool        // never use a personal access token, nor the gh CLI's
	GitHubApp   *github.App // a preconfigured app, shared by every workspace of an operator
	SlackApp    *slack.App  // likewise
	GitHubState string      // carried by installation links, so the cloud knows where GitHub returns

	// Where GitHub and Slack are: "" for github.com and slack.com.
	GitHubAPI, GitHubWeb, SlackAPI, SlackWeb string

	API API // how its HTTP API is served

	// BeforeMigrate runs before the database's pending migrations, if any.
	BeforeMigrate func(pending []string, backup func(path string) error) error
	Log           *slog.Logger
}

// API is how a workspace's HTTP API is served (see api.Server).
type API struct {
	SignIn  bool
	Token   string
	Hosts   []string
	UI      fs.FS
	Version string

	// The cloud's (see api.Server).
	Cloud            bool
	CallbackBase     string
	StatePrefix      string
	SetupClaim       func(ctx context.Context, code string) bool
	SlackInstalled   func(ctx context.Context, teamID string) error
	SlackUninstalled func(ctx context.Context, teamID string)
}

// Runtime is a running workspace.
type Runtime struct {
	DB     *store.Store
	M      *session.Manager
	Auto   *automation.Service
	GitHub *github.Client
	Slack  *slack.Client
	Bot    *slack.Bot
	API    *api.Server

	handler http.Handler
	cancel  context.CancelFunc
	done    chan struct{}
}

// Path is the database file in a workspace's directory.
func Path(dir string) string { return filepath.Join(dir, "beethoven.db") }

// Open opens a workspace's database, migrating it, and starts its
// sessions, automations and pull request tracking, until Close.
func Open(ctx context.Context, cfg Config) (*Runtime, error) {
	db, err := store.OpenWith(Path(cfg.Dir), cfg.Key, store.Options{BeforeMigrate: cfg.BeforeMigrate})
	if err != nil {
		return nil, err
	}
	gh := github.New(cfg.GitHubToken)
	gh.DB, gh.EnvApp, gh.InstallState, gh.NoToken = db, cfg.GitHubApp, cfg.GitHubState, cfg.NoToken
	gh.API, gh.Web = cfg.GitHubAPI, cfg.GitHubWeb
	tokenKey := sha256.Sum256(append([]byte("beethoven tool tokens:"), cfg.Key...))
	m := session.New(session.Config{
		Image:         cfg.Image,
		IdleTimeout:   cfg.IdleTimeout,
		TurnTimeout:   cfg.TurnTimeout,
		Author:        cfg.Author,
		ToolsURL:      cfg.ToolsURL,
		TokenKey:      tokenKey[:],
		Committer:     store.Author{Name: "Beethoven", Email: "beethoven@localhost"},
		Env:           cfg.Env,
		SandboxPrefix: cfg.SandboxPrefix,
	}, db, cfg.Box, agent.Claude{}, gh, cfg.Log)
	auto := automation.New(m, cfg.Log)
	sl := &slack.Client{DB: db, EnvApp: cfg.SlackApp, API: cfg.SlackAPI, Web: cfg.SlackWeb}
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
	}, cfg.Log)

	run, cancel := context.WithCancel(context.WithoutCancel(ctx))
	if err := m.Start(run); err != nil {
		cancel()
		db.Close()
		return nil, err
	}
	rt := &Runtime{DB: db, M: m, Auto: auto, GitHub: gh, Slack: sl, Bot: bot, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(rt.done)
		done := make(chan struct{}, 2)
		go func() { auto.Run(run); done <- struct{}{} }()
		go func() { gh.TrackPullRequests(run, 10*time.Minute, cfg.Log); done <- struct{}{} }()
		<-done
		<-done
	}()

	if cfg.Model != "" {
		if s, err := db.Settings(ctx); err == nil && s.Model == "" { // seed the setting once
			s.Model = cfg.Model
			db.SaveSettings(ctx, s)
		}
	}
	a := cfg.API
	rt.API = &api.Server{M: m, Auto: auto, GitHub: gh, Slack: sl, SlackBot: bot, Log: cfg.Log,
		SignIn: a.SignIn, Token: a.Token, Hosts: a.Hosts, UI: a.UI, Version: a.Version,
		Cloud: a.Cloud, CallbackBase: a.CallbackBase, StatePrefix: a.StatePrefix, SetupClaim: a.SetupClaim,
		SlackInstalled: a.SlackInstalled, SlackUninstalled: a.SlackUninstalled}
	rt.handler = rt.API.Handler()
	return rt, nil
}

// Handler serves the workspace's API, UI and webhooks.
func (rt *Runtime) Handler() http.Handler { return rt.handler }

// Close stops the workspace's work, giving running prompts up to grace to
// wind down, and closes its database.
func (rt *Runtime) Close(grace time.Duration) error {
	rt.cancel()
	<-rt.done
	rt.M.Shutdown(grace)
	return rt.DB.Close()
}
