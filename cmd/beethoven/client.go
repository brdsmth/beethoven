package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/store"
)

// ------------------------------------------------------------------ commands

// repoFlags collects -repo owner/name[@branch] flags.
type repoFlags []store.RepoRef

func (r *repoFlags) String() string { return fmt.Sprint(*r) }
func (r *repoFlags) Set(v string) error {
	repo, branch, _ := strings.Cut(v, "@")
	*r = append(*r, store.RepoRef{Repo: repo, BaseBranch: branch})
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var repos repoFlags
	fs.Var(&repos, "repo", "repository, owner/name[@branch]; repeat for several")
	envName := fs.String("env", "", "environment name")
	model := fs.String("model", "", "model")
	effort := fs.String("effort", "", "effort: low, medium, high, xhigh or max")
	title := fs.String("title", "", "session title")
	detach := fs.Bool("d", false, "don't follow the session")
	fs.Parse(args)
	prompt, err := promptArg(fs.Args())
	if err != nil {
		return err
	}
	body := map[string]any{"prompt": prompt, "repos": []store.RepoRef(repos), "model": *model, "effort": *effort,
		"title": *title, "source": "cli"}
	if *envName != "" {
		e, err := findEnvironment(*envName)
		if err != nil {
			return err
		}
		body["environment_id"] = e.ID
	}
	var out struct {
		Session store.Session `json:"session"`
		Prompt  store.Prompt  `json:"prompt"`
	}
	if err := call("POST", "/api/sessions", body, &out); err != nil {
		return err
	}
	fmt.Printf("session %s  %s/#/s/%s\n", out.Session.ID, serverURL(), out.Session.ID)
	if *detach {
		return nil
	}
	return follow(out.Session.ID, out.Prompt.ID)
}

func cmdSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	detach := fs.Bool("d", false, "don't follow the session")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return errors.New("usage: beethoven send <session> <prompt>")
	}
	id := fs.Arg(0)
	prompt, err := promptArg(fs.Args()[1:])
	if err != nil {
		return err
	}
	var p store.Prompt
	if err := call("POST", "/api/sessions/"+id+"/prompts", map[string]string{"content": prompt, "source": "cli"}, &p); err != nil {
		return err
	}
	if *detach {
		fmt.Println("queued", p.ID)
		return nil
	}
	return follow(id, p.ID)
}

func cmdTail(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: beethoven tail <session>")
	}
	return follow(args[0], "")
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	archived := fs.Bool("a", false, "show archived sessions")
	fs.Parse(args)
	path := "/api/sessions"
	if *archived {
		path += "?archived=1"
	}
	var list []store.Session
	if err := call("GET", path, nil, &list); err != nil {
		return err
	}
	for _, s := range list {
		repo := "(scratch)"
		if p, ok := s.Primary(); ok {
			repo = p.Repo
			if len(s.Repos) > 1 {
				repo += fmt.Sprintf(" +%d", len(s.Repos)-1)
			}
		}
		fmt.Printf("%s  %-8s %-8s %-28s %-6s %s\n", s.ID, s.Status, s.Sandbox, clip(repo, 28), ago(s.UpdatedAt), s.Title)
	}
	return nil
}

func cmdStop(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: beethoven stop <session>")
	}
	return call("POST", "/api/sessions/"+args[0]+"/stop", nil, nil)
}

func cmdSecret(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: beethoven secret set|import|ls|rm …")
	}
	fs := flag.NewFlagSet("secret", flag.ExitOnError)
	scopeFlag := fs.String("scope", "", "empty (global), owner/name, or env:<environment name>")
	fs.Parse(args[1:])
	scope := *scopeFlag
	if name, ok := strings.CutPrefix(scope, "env:"); ok {
		e, err := findEnvironment(name)
		if err != nil {
			return err
		}
		scope = "env:" + e.ID
	}
	switch args[0] {
	case "ls":
		var list []store.SecretInfo
		if err := call("GET", "/api/secrets", nil, &list); err != nil {
			return err
		}
		envs := map[string]string{}
		var all []store.Environment
		call("GET", "/api/environments", nil, &all)
		for _, e := range all {
			envs["env:"+e.ID] = "env:" + e.Name
		}
		for _, s := range list {
			label := s.Scope
			switch {
			case label == "":
				label = "(global)"
			case envs[label] != "":
				label = envs[label]
			}
			fmt.Printf("%-28s %s\n", label, s.Name)
		}
		return nil
	case "set":
		if fs.NArg() != 1 {
			return errors.New("usage: beethoven secret set [-scope s] NAME  (value on stdin)")
		}
		value, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		return call("PUT", "/api/secrets", map[string]string{"scope": scope, "name": fs.Arg(0),
			"value": strings.TrimRight(string(value), "\r\n")}, nil)
	case "import":
		text, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		var out struct {
			Imported []string
			Skipped  []struct {
				Line   int
				Reason string
			}
		}
		if err := call("POST", "/api/secrets/import", map[string]string{"scope": scope, "text": string(text)}, &out); err != nil {
			return err
		}
		fmt.Printf("imported %d: %s\n", len(out.Imported), strings.Join(out.Imported, " "))
		for _, s := range out.Skipped {
			fmt.Printf("skipped line %d: %s\n", s.Line, s.Reason)
		}
		return nil
	case "rm":
		if fs.NArg() != 1 {
			return errors.New("usage: beethoven secret rm [-scope s] NAME")
		}
		return call("DELETE", "/api/secrets?scope="+url.QueryEscape(scope)+"&name="+url.QueryEscape(fs.Arg(0)), nil, nil)
	}
	return errors.New("usage: beethoven secret set|import|ls|rm …")
}

func cmdEnv(args []string) error {
	if len(args) != 1 || args[0] != "ls" {
		return errors.New("usage: beethoven env ls")
	}
	var list []store.Environment
	if err := call("GET", "/api/environments", nil, &list); err != nil {
		return err
	}
	for _, e := range list {
		fmt.Printf("%-24s %s\n", e.Name, strings.Join(store.RepoNames(e.Repos), ", "))
	}
	return nil
}

func findEnvironment(name string) (store.Environment, error) {
	var list []store.Environment
	if err := call("GET", "/api/environments", nil, &list); err != nil {
		return store.Environment{}, err
	}
	for _, e := range list {
		if strings.EqualFold(e.Name, name) || e.ID == name {
			return e, nil
		}
	}
	return store.Environment{}, fmt.Errorf("no environment named %q", name)
}

// promptArg joins the arguments, or reads stdin when they are absent or "-".
func promptArg(args []string) (string, error) {
	if len(args) == 0 || (len(args) == 1 && args[0] == "-") {
		b, err := io.ReadAll(os.Stdin)
		return strings.TrimSpace(string(b)), err
	}
	return strings.Join(args, " "), nil
}

// ------------------------------------------------------------------- stream

// follow prints a session's timeline. With untilPrompt set it prints from
// that prompt onward and returns once it finishes, with an error if it did
// not succeed.
func follow(sessionID, untilPrompt string) error {
	var last int64
	quiet := untilPrompt != ""
	for attempt := 0; ; attempt++ {
		done, err := streamOnce(sessionID, untilPrompt, &last, &quiet)
		if done || attempt > 30 {
			return err
		}
		time.Sleep(time.Second) // reconnect and resume after the last event seen
	}
}

func streamOnce(sessionID, untilPrompt string, last *int64, quiet *bool) (bool, error) {
	req, _ := http.NewRequest("GET", serverURL()+"/api/sessions/"+sessionID+"/events", nil)
	authorize(req)
	if *last > 0 {
		req.Header.Set("Last-Event-ID", fmt.Sprint(*last))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true, apiError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var e store.Event
		if json.Unmarshal([]byte(data), &e) != nil {
			continue
		}
		if e.Seq > 0 {
			*last = e.Seq
		}
		if *quiet && e.Type == "prompt" && e.Prompt == untilPrompt {
			*quiet = false
		}
		if !*quiet {
			render(e)
		}
		if e.Type == "prompt.end" && untilPrompt != "" {
			var d struct{ ID, Status, Error string }
			json.Unmarshal(e.Data, &d)
			if d.ID == untilPrompt {
				if d.Status != store.Done {
					return true, fmt.Errorf("prompt %s: %s", d.Status, d.Error)
				}
				return true, nil
			}
		}
	}
	return false, sc.Err()
}

const (
	dim   = "\x1b[2m"
	bold  = "\x1b[1m"
	red   = "\x1b[31m"
	green = "\x1b[32m"
	cyan  = "\x1b[36m"
	reset = "\x1b[0m"
)

func render(e store.Event) {
	var d map[string]any
	json.Unmarshal(e.Data, &d)
	s := func(k string) string { v, _ := d[k].(string); return v }
	switch e.Type {
	case "prompt":
		author, _ := d["author"].(map[string]any)
		name, _ := author["name"].(string)
		fmt.Printf("\n%s› %s%s %s\n", bold, name, reset, s("content"))
	case "sandbox":
		if s("state") == "failed" {
			fmt.Printf("%s✗ sandbox failed: %s%s\n", red, s("message"), reset)
		} else if s("phase") != "" || s("state") != "starting" {
			fmt.Printf("%s· sandbox %s %s%s\n", dim, s("state"), s("phase"), reset)
		}
	case "text":
		if s("parent") == "" {
			fmt.Printf("\n%s\n", strings.TrimSpace(s("text")))
		}
	case "tool":
		if s("parent") == "" {
			fmt.Printf("%s▸ %s%s %s\n", cyan, s("name"), reset, clip(toolSummary(d["input"]), 120))
		}
	case "tool.result":
		if d["is_error"] == true {
			line, _, _ := strings.Cut(strings.TrimSpace(s("output")), "\n")
			fmt.Printf("  %s✗ %s%s\n", red, clip(line, 160), reset)
		}
	case "artifact":
		fmt.Printf("%s★ %s: %s%s\n", green, s("kind"), s("url"), reset)
	case "warning":
		fmt.Printf("%s! %s%s\n", red, s("message"), reset)
	case "child":
		fmt.Printf("%s↳ child %s %s: %s%s\n", dim, s("id"), s("status"), s("title"), reset)
	case "prompt.end":
		cost, _ := d["cost_usd"].(float64)
		ms, _ := d["duration_ms"].(float64)
		took := (time.Duration(ms) * time.Millisecond).Round(time.Second)
		switch s("status") {
		case store.Done:
			fmt.Printf("\n%s✓ done%s %s($%.2f, %s)%s\n", green, reset, dim, cost, took, reset)
		default:
			fmt.Printf("\n%s✗ %s%s %s\n", red, s("status"), reset, s("error"))
		}
	}
}

// toolSummary picks the most telling argument of a tool call.
func toolSummary(input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt", "title"} {
		if v, ok := m[k].(string); ok && v != "" {
			return strings.ReplaceAll(v, "\n", " ")
		}
	}
	return ""
}

// -------------------------------------------------------------------- http

func serverURL() string { return strings.TrimRight(env("BEETHOVEN_URL", "http://127.0.0.1:7070"), "/") }

func authorize(req *http.Request) {
	if t := os.Getenv("BEETHOVEN_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
}

func call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, serverURL()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	authorize(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w (is `beethoven serve` running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return apiError(resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func apiError(resp *http.Response) error {
	var e struct{ Error string }
	json.NewDecoder(resp.Body).Decode(&e)
	if e.Error == "" {
		e.Error = resp.Status
	}
	return errors.New(e.Error)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
