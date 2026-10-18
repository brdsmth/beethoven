package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/brdsmth/beethoven/internal/cloud"
	"github.com/brdsmth/beethoven/internal/sandbox"
	"github.com/brdsmth/beethoven/internal/store"
	"github.com/brdsmth/beethoven/internal/workspace"
	"github.com/brdsmth/beethoven/web"
)

// serveCloud runs the hosted service: many workspaces, each at
// <slug>.<domain>, with the operator's GitHub and Slack apps.
func serveCloud(ctx context.Context, log *slog.Logger, addr, port, dataDir string, masterKey []byte, domain string,
	base workspace.Config) error {
	scheme, publicPort := env("BEETHOVEN_CLOUD_SCHEME", "https"), ""
	if domain == "localhost" { // *.localhost reaches this machine, for trying it out
		scheme, publicPort = env("BEETHOVEN_CLOUD_SCHEME", "http"), port
	}
	admin := os.Getenv("BEETHOVEN_ADMIN_TOKEN")
	if len(admin) < 16 {
		return errors.New("BEETHOVEN_ADMIN_TOKEN must be set, to at least 16 characters: it's the password for the admin pages")
	}
	// Every workspace uses the service's apps: workspaces can't make their own.
	if base.GitHubApp == nil {
		log.Warn("no GitHub App: set BEETHOVEN_GITHUB_APP for workspaces to reach GitHub")
	}
	if base.SlackApp == nil {
		log.Warn("no Slack app: set BEETHOVEN_SLACK_APP for workspaces to add Beethoven to Slack")
	}
	// Commits fall back to a neutral author, not this machine's git identity:
	// people's sessions commit as them.
	if os.Getenv("BEETHOVEN_GIT_EMAIL") == "" {
		base.Author = store.Author{Name: env("BEETHOVEN_GIT_NAME", "Beethoven"), Email: "noreply@" + domain}
	}
	appURL := scheme + "://app." + domain
	if publicPort != "" {
		appURL += ":" + publicPort
	}
	base.API = workspace.API{UI: web.FS(), Version: version}

	// One provider for Docker; on Modal, an app per workspace, so its
	// sandboxes and their cost are its own.
	kind := os.Getenv("BEETHOVEN_SANDBOX")
	if kind == "" && os.Getenv("MODAL_TOKEN_ID") != "" {
		kind = "modal"
	}
	var shared sandbox.Provider
	toolsBase := env("BEETHOVEN_SANDBOX_URL", appURL)
	if kind != "modal" {
		box, url, err := sandboxes(ctx, log, base.Image, port, appURL, "")
		if err != nil {
			return err
		}
		shared, toolsBase = box, url
	}
	var mu sync.Mutex
	modal := map[string]sandbox.Provider{}
	prefix := env("BEETHOVEN_MODAL_APP", "beethoven")
	providers := func(slug string) (sandbox.Provider, error) {
		if shared != nil {
			return shared, nil
		}
		mu.Lock()
		defer mu.Unlock()
		if p, ok := modal[slug]; ok {
			return p, nil
		}
		m, err := sandbox.NewModal(prefix + "-" + slug)
		if err != nil {
			return nil, fmt.Errorf("modal: %w", err)
		}
		go func() {
			if err := m.Warm(ctx); err != nil && ctx.Err() == nil {
				log.Warn("modal: building the sandbox image", "workspace", slug, "err", err)
			}
		}()
		modal[slug] = m
		return m, nil
	}

	svc, err := cloud.New(cloud.Config{
		Domain: domain, Scheme: scheme, Port: publicPort, DataDir: dataDir, MasterKey: masterKey, AdminToken: admin,
		Workspace: base, Sandboxes: providers, ToolsBase: strings.TrimSuffix(toolsBase, "/"), Log: log,
	})
	if err != nil {
		return err
	}
	if err := svc.Start(ctx); err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: svc, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("beethoven cloud is running", "listen", "http://"+addr, "service", appURL, "workspaces", strings.Replace(appURL, "://app.", "://<slug>.", 1),
		"admin", appURL+"/admin", "data", dataDir)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	svc.Close(15 * time.Second)
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
