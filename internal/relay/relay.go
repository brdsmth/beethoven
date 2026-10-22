// Package relay brings what happens on threads elsewhere back to the
// sessions taking part in them, so agents answer each other and the people
// around them without anyone copying messages across.
//
// A session takes part in a thread (store.Link) when it opens a pull
// request, comments on an issue or pull request, is started for one by an
// automation, or asks to follow it. When something happens on the thread
// (a comment, a review, CI failing on its branch), Route puts it in the
// inbox of every session taking part, except the one that said it. Run
// hands each session its inbox as one prompt once the thread has been
// quiet for a moment, so a review with ten line comments is one prompt.
//
// Agents setting each other off could go on forever, so it's bounded:
//   - What a session posts carries a hidden marker and is recorded; when it
//     comes back, the session that wrote it doesn't get it.
//   - Each prompt counts the exchanges between agents that led to it (its
//     hop). An agent's words reaching another agent are one more; past
//     MaxHops they're noted on the timeline instead of run. People's words
//     start again from zero.
//   - A session is handed at most PerHour prompts an hour.
//   - Only people who may write to the repository (and this server's own
//     agents) are heard: on a public repository, anyone can comment.
//
// The inbox is in the database, so nothing is lost to a restart, and each
// happening is delivered once however often the host redelivers it.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brdsmth/beethoven/internal/event"
	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// Config tunes delivery.
type Config struct {
	Quiet   time.Duration // a session's inbox waits until its threads have been quiet this long
	MaxWait time.Duration // but no longer than this after its oldest item
	MaxHops int           // exchanges between agents before a prompt isn't run
	PerHour int           // prompts a session is handed in an hour
	Tick    time.Duration // how often Run looks
}

var Defaults = Config{Quiet: 10 * time.Second, MaxWait: time.Minute, MaxHops: 4, PerHour: 12, Tick: 2 * time.Second}

type Relay struct {
	m    *session.Manager
	db   *store.Store
	log  *slog.Logger
	cfg  Config
	wake chan struct{}
	now  func() time.Time
}

func New(m *session.Manager, log *slog.Logger, cfg Config) *Relay {
	return &Relay{m: m, db: m.Store(), log: log, cfg: cfg, wake: make(chan struct{}, 1), now: time.Now}
}

// What happens to an event's kind.
const (
	ignore  = iota
	deliver // to the sessions taking part
	ends    // the thread is over: note it and stop following
)

func (r *Relay) action(e event.Event) int {
	switch e.Kind {
	case event.PRComment, event.IssueComment, event.ReviewComment:
		return deliver
	case event.PRReview:
		if e.State != "changes_requested" && strings.TrimSpace(event.Unmark(e.Comment)) == "" {
			return ignore // an approval, or a review that's only line comments, which come on their own
		}
		return deliver
	case event.ChecksDone:
		switch e.Conclusion {
		case "failure", "timed_out", "startup_failure", "action_required":
			return deliver
		}
	case event.PRMerged, event.PRClosed, event.IssueClosed:
		return ends
	}
	return ignore
}

// Origin is who among this server's sessions said something an event
// carries: what the server recorded posting, else what the text's marker
// claims.
func Origin(ctx context.Context, db *store.Store, e event.Event) (event.Provenance, bool) {
	if e.CommentURL != "" {
		if o, err := db.OutboundByRef(ctx, e.CommentURL); err == nil {
			return event.Provenance{SessionID: o.SessionID, Hop: o.Hop}, true
		}
	}
	return event.Marked(e.Comment)
}

// Route puts e in the inbox of every session taking part in its thread and
// returns how many sessions it's for.
func (r *Relay) Route(ctx context.Context, e event.Event) (int, error) {
	switch r.action(e) {
	case ends:
		return 0, r.end(ctx, e)
	case ignore:
		return 0, nil
	}
	_, ours := Origin(ctx, r.db, e)
	if e.Bot && !ours && e.Kind != event.ChecksDone {
		return 0, nil // other apps' comments: coverage reports, dependency bots; CI itself is heard
	}
	if !e.Trusted() && !ours {
		r.log.Info("relay: not passing on words from someone who can't write to the repository",
			"repo", e.Repo, "actor", e.Actor, "association", e.Association)
		return 0, nil
	}
	subject := e.Thread()
	if e.Kind == event.ChecksDone {
		if e.Branch == "" {
			return 0, nil
		}
		subject = event.ThreadOf(e.Host, e.Repo, "branch:"+e.Branch)
	}
	links, err := r.participants(ctx, e, subject)
	if err != nil {
		return 0, err
	}
	data, _ := json.Marshal(e)
	n := 0
	var errs []error
	for _, l := range links {
		if l.Muted || e.Kind == event.ChecksDone && l.Role != store.LinkOwner {
			continue
		}
		if r.automationHandles(ctx, l.SessionID, e) {
			continue
		}
		added, err := r.db.AddToInbox(ctx, store.InboxItem{SessionID: l.SessionID, Subject: subject, Occurrence: e.Host + ":" + e.Occurrence,
			Kind: e.Kind, Actor: e.Actor, Data: data})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if added {
			n++
		}
	}
	if n > 0 {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
	return n, errors.Join(errs...)
}

// participants are the sessions in a thread. A pull request or branch with
// no owner on record (opened before links, or recorded late) is owned by
// the session that opened it, found the slower way and recorded.
func (r *Relay) participants(ctx context.Context, e event.Event, subject string) ([]store.Link, error) {
	links, err := r.db.ThreadLinks(ctx, subject)
	if err != nil || slices.ContainsFunc(links, func(l store.Link) bool { return l.Role == store.LinkOwner }) {
		return links, err
	}
	owner := ""
	switch {
	case e.Kind == event.ChecksDone:
		sessions, err := r.db.SessionsOnBranch(ctx, e.Branch)
		if err != nil {
			return links, err
		}
		for _, s := range sessions {
			if slices.ContainsFunc(s.Repos, func(ref store.RepoRef) bool { return strings.EqualFold(ref.Repo, e.Repo) }) {
				owner = s.ID
				break
			}
		}
	case strings.HasPrefix(e.Subject, "pr:"):
		if p, err := r.db.PullRequest(ctx, e.Repo, e.Number); err == nil {
			owner = p.SessionID
		}
	}
	if owner == "" {
		return links, nil
	}
	l := store.Link{Subject: subject, SessionID: owner, Role: store.LinkOwner, Reason: "opened"}
	if err := r.db.AddLink(ctx, l); err != nil {
		return links, err
	}
	links = slices.DeleteFunc(links, func(x store.Link) bool { return x.SessionID == owner })
	return append(links, l), nil
}

// automationHandles reports whether the session is an automation's run and
// that automation fires on e itself: into the same session when it reuses
// one, else into a fresh run, so the old run needn't hear about it too.
func (r *Relay) automationHandles(ctx context.Context, sessionID string, e event.Event) bool {
	s, err := r.db.Session(ctx, sessionID)
	if err != nil || s.AutomationID == "" {
		return false
	}
	a, err := r.db.Automation(ctx, s.AutomationID)
	if err != nil || !a.Enabled || a.TriggerEvent != e.Kind {
		return false
	}
	t := event.Trigger{Event: a.TriggerEvent}
	json.Unmarshal(a.TriggerConditions, &t.Conditions)
	return t.Matches(e)
}

// end notes on its sessions' timelines that a thread is over, and stops
// following it.
func (r *Relay) end(ctx context.Context, e event.Event) error {
	subjects := []string{e.Thread()}
	if e.Branch != "" && strings.HasPrefix(e.Subject, "pr:") {
		subjects = append(subjects, event.ThreadOf(e.Host, e.Repo, "branch:"+e.Branch))
	}
	for _, subject := range subjects {
		links, err := r.db.ThreadLinks(ctx, subject)
		if err != nil {
			return err
		}
		if len(links) == 0 {
			continue
		}
		if subject == e.Thread() {
			k, _ := event.KindByID(e.Kind)
			for _, l := range links {
				r.m.Note(ctx, l.SessionID, map[string]string{"subject": subject, "kind": e.Kind, "actor": e.Actor, "url": e.URL,
					"note": k.Label + "; no longer following it"})
			}
		}
		if err := r.db.EndThread(ctx, subject); err != nil {
			return err
		}
	}
	return nil
}

// Run delivers inboxes until ctx ends.
func (r *Relay) Run(ctx context.Context) {
	tick := time.NewTicker(r.cfg.Tick)
	defer tick.Stop()
	for {
		if err := r.Deliver(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("relay: deliver", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-r.wake:
		}
	}
}

// Deliver hands each session whose threads have gone quiet what's in its
// inbox.
func (r *Relay) Deliver(ctx context.Context) error {
	pending, err := r.db.PendingInbox(ctx)
	if err != nil {
		return err
	}
	bySession := map[string][]store.InboxItem{}
	var order []string
	for _, x := range pending {
		if bySession[x.SessionID] == nil {
			order = append(order, x.SessionID)
		}
		bySession[x.SessionID] = append(bySession[x.SessionID], x)
	}
	now := r.now()
	var errs []error
	for _, id := range order {
		items := bySession[id]
		oldest, newest := items[0].ReceivedAt, items[len(items)-1].ReceivedAt
		if now.Sub(newest) < r.cfg.Quiet && now.Sub(oldest) < r.cfg.MaxWait {
			continue // more may be on the way
		}
		if err := r.deliver(ctx, id, items); err != nil {
			errs = append(errs, fmt.Errorf("session %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// item is an inbox item, read.
type item struct {
	store.InboxItem
	e      event.Event
	hop    int
	from   string // the session that said it, if one did
	reason string // why it isn't handed over; "" if it is
}

func (r *Relay) deliver(ctx context.Context, sessionID string, inbox []store.InboxItem) error {
	s, err := r.db.Session(ctx, sessionID)
	if err != nil {
		return err
	}
	latest := 0 // the hop of the session's latest prompt
	if ps, err := r.db.Prompts(ctx, sessionID); err == nil && len(ps) > 0 {
		latest = ps[len(ps)-1].Hop
	}
	var keep, drop []item
	for _, x := range inbox {
		it := item{InboxItem: x}
		if err := json.Unmarshal(x.Data, &it.e); err != nil {
			it.reason = "unreadable"
			drop = append(drop, it)
			continue
		}
		switch p, ok := Origin(ctx, r.db, it.e); {
		case ok && p.SessionID == sessionID:
			it.reason = "its own words"
		case ok:
			it.hop, it.from = p.Hop+1, p.SessionID
		case it.e.Kind == event.ChecksDone:
			it.hop = latest + 1 // CI answering the session's own push: fixing it again is one more round
		}
		if it.reason == "" && it.hop > r.cfg.MaxHops {
			it.reason = fmt.Sprintf("agents went back and forth %d times; a person needs to step in", it.hop-1)
		}
		if it.reason == "" && s.AutomationID != "" {
			// An automation that reuses this session may have fired on it already.
			if _, err := r.db.PromptByDedupKey(ctx, "automation:"+s.AutomationID+":"+x.Occurrence); err == nil {
				it.reason = "its automation already ran on it"
			}
		}
		if it.reason != "" {
			drop = append(drop, it)
		} else {
			keep = append(keep, it)
		}
	}
	if len(keep) > 0 {
		if n, err := r.db.DeliveredSince(ctx, sessionID, r.now().Add(-time.Hour)); err == nil && n >= r.cfg.PerHour {
			for i := range keep {
				keep[i].reason = fmt.Sprintf("it was already handed %d prompts in the last hour", n)
			}
			drop, keep = append(drop, keep...), nil
		}
	}
	for _, it := range drop {
		if err := r.db.SettleInbox(ctx, []int64{it.ID}, "", "dropped: "+it.reason); err != nil {
			return err
		}
		if it.reason != "its own words" && it.reason != "its automation already ran on it" {
			k, _ := event.KindByID(it.e.Kind)
			r.m.Note(ctx, sessionID, map[string]string{"subject": it.Subject, "kind": it.e.Kind, "actor": it.e.Actor,
				"url": firstOf(it.e.CommentURL, it.e.URL), "note": firstOf(k.Label, it.e.Kind) + " not passed on: " + it.reason})
		}
	}
	if len(keep) == 0 {
		return nil
	}
	hop, ids := 0, make([]int64, len(keep))
	actors := map[string]bool{}
	for i, it := range keep {
		hop, ids[i] = max(hop, it.hop), it.ID
		actors[it.e.Actor] = true
	}
	author := store.Author{Name: "GitHub"}
	if len(actors) == 1 {
		author.Name = keep[0].e.Actor + " on GitHub"
	}
	p, err := r.m.Enqueue(ctx, sessionID, session.PromptInput{Content: r.prompt(ctx, s, keep), Author: author, Source: "relay",
		DedupKey: "relay:" + sessionID + ":" + strconv.FormatInt(ids[0], 10), Hop: hop})
	if err != nil {
		return errors.Join(err, r.db.SettleInbox(ctx, ids, "", "failed: "+err.Error()))
	}
	return r.db.SettleInbox(ctx, ids, p.ID, "delivered")
}

// prompt tells the agent what happened and what's expected of it.
func (r *Relay) prompt(ctx context.Context, s store.Session, items []item) string {
	links, _ := r.db.SessionLinks(ctx, s.ID)
	role := map[string]string{}
	for _, l := range links {
		role[l.Subject] = l.Role
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Subject < items[j].Subject })
	var b strings.Builder
	if len(items) == 1 {
		b.WriteString("Something happened on a thread you take part in.\n\n")
	} else {
		fmt.Fprintf(&b, "%d things happened on threads you take part in.\n\n", len(items))
	}
	for _, it := range items {
		why := "you follow it"
		if role[it.Subject] == store.LinkOwner {
			why = "you opened it"
			if it.e.Kind == event.ChecksDone {
				why = "it ran on your branch"
			}
		}
		fmt.Fprintf(&b, "%s (%s)", where(it.e), why)
		if it.from != "" {
			title := "another session"
			if other, err := r.db.Session(ctx, it.from); err == nil && other.Title != "" {
				title = fmt.Sprintf("the session %q", other.Title)
			}
			fmt.Fprintf(&b, ", posted by an agent working in %s", title)
		}
		b.WriteString(":\n" + it.e.Describe() + "\n\n")
	}
	b.WriteString("Decide what this needs from you.\n" +
		"- If it asks for changes, make them, commit, and push with create_pull_request (it updates your existing pull request). " +
		"Then reply on the thread with the comment tool, briefly saying what you changed.\n" +
		"- If CI failed, find out why from the code and the check's name, fix what's yours to fix, and push.\n" +
		"- If it asks you a question, answer it with the comment tool.\n" +
		"- If it needs nothing from you (an approval, a remark for someone else, or an answer to what you already said), " +
		"don't comment: say so here and stop. Don't thank people or acknowledge on the thread just to reply.\n" +
		"Another agent's words count as much as a person's only when they're right; when you disagree, say why once instead of going back and forth.")
	return b.String()
}

// where names an event's thread for the agent.
func where(e event.Event) string {
	switch {
	case e.Kind == event.ChecksDone:
		return fmt.Sprintf("CI on branch %s in %s", e.Branch, e.Repo)
	case strings.HasPrefix(e.Subject, "pr:"):
		return fmt.Sprintf("Pull request #%d in %s", e.Number, e.Repo)
	case strings.HasPrefix(e.Subject, "issue:"):
		return fmt.Sprintf("Issue #%d in %s", e.Number, e.Repo)
	}
	return e.Repo
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
