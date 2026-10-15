package slack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/brdsmth/beethoven/internal/session"
	"github.com/brdsmth/beethoven/internal/store"
)

// file is a file shared in a message, as events and files.info describe it.
type file struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Title              string `json:"title"`
	Mimetype           string `json:"mimetype"`
	Size               int64  `json:"size"`
	Mode               string `json:"mode"` // hosted, snippet, post or external; tombstone once deleted
	IsExternal         bool   `json:"is_external"`
	URLPrivateDownload string `json:"url_private_download"`
}

func (f file) name() string { return firstOf(f.Name, f.Title, f.ID) }

// fetched is what became of the files a request shares.
type fetched struct {
	attachments []store.Attachment
	missed      []string // "name (why)", for the files left out
	noAccess    bool     // the app may not read files: it lacks files:read
}

// fetch downloads the files a request shares, in order, as far as the
// attachment limits allow.
func (b *Bot) fetch(ctx context.Context, in store.SlackInstallation, files []file) fetched {
	var out fetched
	var total int64
	seen := map[string]bool{}
	for _, f := range files {
		if f.ID == "" || seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		miss := func(why string) { out.missed = append(out.missed, f.name()+" ("+why+")") }
		if out.noAccess {
			miss("the Slack app can't read files")
			continue
		}
		if len(out.attachments) == session.MaxAttachments {
			miss(fmt.Sprintf("over the limit of %d files", session.MaxAttachments))
			continue
		}
		// The event's copy may be a stub (Slack Connect); files.info has it all.
		info, err := b.Slack.fileInfo(ctx, in.BotToken, f.ID)
		switch {
		case errorCode(err) == "missing_scope":
			out.noAccess = true
			miss("the Slack app can't read files")
			continue
		case err != nil:
			b.Log.Warn("slack: looking up a file", "file", f.ID, "err", err)
			miss("Slack couldn't find it")
			continue
		}
		f = info
		switch {
		case f.IsExternal || f.Mode == "external":
			miss("a link to a file stored elsewhere")
		case f.Mode == "tombstone" || f.URLPrivateDownload == "":
			miss("no longer available")
		case total+f.Size > session.MaxAttachmentBytes:
			miss(fmt.Sprintf("over the limit of %d MB in all", session.MaxAttachmentBytes>>20))
		default:
			data, err := b.Slack.download(ctx, in.BotToken, f, session.MaxAttachmentBytes-total)
			if err != nil {
				b.Log.Warn("slack: downloading a file", "file", f.ID, "err", err)
				miss("the download failed")
				continue
			}
			total += int64(len(data))
			out.attachments = append(out.attachments, store.Attachment{Name: f.name(), ContentType: f.Mimetype, Data: data})
		}
	}
	return out
}

func (c *Client) fileInfo(ctx context.Context, token, id string) (file, error) {
	var out struct {
		File file `json:"file"`
	}
	err := c.call(ctx, token, "files.info", url.Values{"file": {id}}, &out)
	return out.File, err
}

var errTooLarge = errors.New("file too large")

// download fetches a file's content with the bot token, refusing more than
// max bytes. The token goes to Slack's own hosts only.
func (c *Client) download(ctx context.Context, token string, f file, max int64) ([]byte, error) {
	u, err := url.Parse(f.URLPrivateDownload)
	if err != nil || !c.slackHost(u) {
		return nil, fmt.Errorf("not a Slack file URL: %s", f.URLPrivateDownload)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	hc := &http.Client{Transport: c.http().Transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if c.slackHost(r.URL) {
			r.Header.Set("Authorization", "Bearer "+token)
		} else {
			r.Header.Del("Authorization")
		}
		return nil
	}}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: %s", resp.Status)
	}
	// Without access, Slack answers with its sign-in page.
	if t, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); t == "text/html" && !strings.HasPrefix(f.Mimetype, "text/html") {
		return nil, errors.New("download: Slack sent a web page instead of the file")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err == nil && int64(len(data)) > max {
		err = errTooLarge
	}
	return data, err
}

// slackHost reports whether u is Slack's: an HTTPS slack.com address, or
// the API's own host (a stand-in, in tests).
func (c *Client) slackHost(u *url.URL) bool {
	if api, err := url.Parse(c.api()); err == nil && u.Scheme == api.Scheme && u.Host == api.Host {
		return true
	}
	h := u.Hostname()
	return u.Scheme == "https" && (h == "slack.com" || strings.HasSuffix(h, ".slack.com"))
}

// noFileAccess tells people how to let the app read files.
func noFileAccess(app *App) string {
	where := "in the app's settings on api.slack.com"
	if app != nil {
		where = "in <https://api.slack.com/apps/" + app.AppID + "/oauth|the app's settings>"
	}
	return ":warning: I can't open files shared in Slack yet: the Beethoven app needs the `files:read` scope. " +
		"Add it under *OAuth & Permissions* " + where + ", then reinstall the app and mention me again."
}
