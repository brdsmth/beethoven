package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/brdsmth/beethoven/internal/store"
)

// cmdLogs prints a session's whole stored timeline and exits: a plain
// transcript with full tool calls and output, made to hand to an agent
// diagnosing what went wrong, or the raw events with -json.
func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the raw events as a JSON array")
	limit := fs.Int("max", 4000, "clip each tool input and output to this many characters (0: no limit)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: beethoven logs [-json] [-max n] <session>")
	}
	id := fs.Arg(0)
	var sess store.Session
	if err := call("GET", "/api/sessions/"+id, nil, &sess); err != nil {
		return err
	}
	evs, err := fetchEvents(id)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(evs)
	}
	transcript(os.Stdout, sess, evs, *limit)
	return nil
}

// fetchEvents reads every stored event, a page at a time.
func fetchEvents(id string) ([]store.Event, error) {
	all := []store.Event{}
	for after := int64(0); ; {
		var page []store.Event
		if err := call("GET", fmt.Sprintf("/api/sessions/%s/events?format=json&after=%d", id, after), nil, &page); err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return all, nil
		}
		all = append(all, page...)
		after = page[len(page)-1].Seq
	}
}

// transcript writes the timeline as plain text. Unlike tail it keeps what
// a diagnosis needs: timestamps, every tool call with its input and output,
// subagent activity, and sandbox phases.
func transcript(w io.Writer, sess store.Session, evs []store.Event, limit int) {
	fmt.Fprintf(w, "Session %s: %s\n", sess.ID, sess.Title)
	fmt.Fprintf(w, "Status: %s  Source: %s  Branch: %s  Created: %s\n", sess.Status, sess.Source, sess.Branch,
		sess.CreatedAt.Format("2006-01-02 15:04:05 MST"))
	for _, r := range sess.Repos {
		fmt.Fprintf(w, "Repository: %s", r.Repo)
		if r.BaseBranch != "" {
			fmt.Fprintf(w, " (from %s)", r.BaseBranch)
		}
		if r.PRURL != "" {
			fmt.Fprintf(w, "  PR: %s", r.PRURL)
		}
		fmt.Fprintln(w)
	}
	if sess.ParentID != "" {
		fmt.Fprintf(w, "Parent session: %s\n", sess.ParentID)
	}
	cut := func(s string) string {
		if limit > 0 {
			return clip(s, limit)
		}
		return s
	}
	for _, e := range evs {
		var d map[string]any
		json.Unmarshal(e.Data, &d)
		s := func(k string) string { v, _ := d[k].(string); return v }
		at := e.At.Format("15:04:05")
		sub := ""
		if s("parent") != "" {
			sub = " [subagent]"
		}
		switch e.Type {
		case "prompt":
			author, _ := d["author"].(map[string]any)
			name, _ := author["name"].(string)
			fmt.Fprintf(w, "\n=== %s prompt %s from %s (%s)\n%s\n", at, s("id"), name, s("source"), s("content"))
		case "prompt.start":
		case "sandbox":
			fmt.Fprintf(w, "%s sandbox %s %s %s\n", at, s("state"), s("phase"), s("message"))
		case "text":
			fmt.Fprintf(w, "\n%s assistant%s:\n%s\n", at, sub, strings.TrimSpace(s("text")))
		case "tool":
			in, _ := json.Marshal(d["input"])
			fmt.Fprintf(w, "\n%s tool call%s %s %s\n", at, sub, s("name"), cut(string(in)))
		case "tool.result":
			label := "result"
			if d["is_error"] == true {
				label = "ERROR"
			}
			fmt.Fprintf(w, "%s tool %s%s:\n%s\n", at, label, sub, cut(strings.TrimRight(s("output"), "\n")))
		case "artifact":
			fmt.Fprintf(w, "%s artifact %s: %s\n", at, s("kind"), s("url"))
		case "warning":
			fmt.Fprintf(w, "%s WARNING: %s\n", at, s("message"))
		case "child":
			fmt.Fprintf(w, "%s child session %s %s: %s\n", at, s("id"), s("status"), s("title"))
		case "prompt.end":
			cost, _ := d["cost_usd"].(float64)
			fmt.Fprintf(w, "=== %s prompt %s ended %s ($%.2f) %s\n", at, s("id"), s("status"), cost, s("error"))
		default:
			fmt.Fprintf(w, "%s %s %s\n", at, e.Type, cut(string(e.Data)))
		}
	}
}
