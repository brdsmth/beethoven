import { useEffect, useState } from 'preact/hooks';
import { api, get, type GitHubStatus } from './api';
import { Spinner, errText, fail, useLoad } from './util';

// Connecting GitHub: register the server's GitHub App (unless one is
// preconfigured), authorize it, install it. Each step is a trip to
// github.com; GitHub returns the browser to /?github=<step>, and
// GitHubReturn completes the step through the API, then moves on.

const SETTINGS = '/#/settings/github';

/** Sends the browser to GitHub to authorize the app. */
export async function authorizeGitHub() {
  const r = await api<{ url: string }>('POST', '/api/github/authorize', { base_url: location.origin });
  location.assign(r.url);
}

/** Registers the server's own app: GitHub's form, already filled in. */
async function registerApp(org: string, isPublic: boolean) {
  const r = await api<{ url: string; manifest: string }>('POST', '/api/github/app', { base_url: location.origin, org, public: isPublic });
  const form = Object.assign(document.createElement('form'), { method: 'post', action: r.url });
  form.append(Object.assign(document.createElement('input'), { type: 'hidden', name: 'manifest', value: r.manifest }));
  document.body.append(form);
  form.submit();
}

/** The step GitHub returned the browser from, if it did. */
export const githubStep = () => new URLSearchParams(location.search).get('github');

export function GitHubReturn() {
  const [err, setErr] = useState('');
  useEffect(() => {
    const q = new URLSearchParams(location.search);
    const step = q.get('github'), code = q.get('code') || '', state = q.get('state') || '';
    (async () => {
      if (q.get('error')) throw new Error(q.get('error_description') || 'GitHub did not complete the request.');
      if (step === 'created') {
        const r = await api<{ next: string }>('POST', '/api/github/app/created', { code, state });
        return location.replace(r.next); // on to authorizing it
      }
      if (step === 'authorized') {
        const r = await api<{ next: string }>('POST', '/api/github/account', { code, state });
        return location.replace(r.next || SETTINGS); // on to installing it, unless it already is
      }
      if (step === 'installed') {
        const st = await get<GitHubStatus>('/api/github');
        return st.account ? location.replace(SETTINGS) : authorizeGitHub();
      }
      location.replace('/');
    })().catch(e => setErr(errText(e)));
  }, []);
  return (
    <div class="page narrow">
      <h1>Connecting GitHub</h1>
      {err ? <><p class="err" role="alert">{err}</p><p><a href={SETTINGS}>Back to GitHub settings</a></p></> : <p class="muted"><Spinner /> One moment…</p>}
    </div>
  );
}

/** The next step towards a connected GitHub account, wherever the server
 * stands: without an app, with one nobody has authorized, or connected. */
export function ConnectGitHub({ status }: { status: GitHubStatus }) {
  const [owner, setOwner] = useState<'me' | 'org'>('me');
  const [org, setOrg] = useState('');
  const [shared, setShared] = useState(false);
  const [busy, setBusy] = useState(false);
  const run = (f: () => Promise<void>) => async () => {
    setBusy(true);
    try { await f(); } catch (e) { fail(e); setBusy(false); } // success leaves the page
  };
  if (status.account) return null;
  if (status.app) {
    return (
      <div class="stack">
        <p class="muted">Sign in with GitHub to let agents work on your repositories. Pull requests open as you.</p>
        <div><button class="btn primary" disabled={busy} onClick={run(authorizeGitHub)}>{busy ? 'Opening GitHub…' : 'Connect GitHub'}</button></div>
      </div>
    );
  }
  return (
    <form class="stack" onSubmit={e => { e.preventDefault(); run(() => registerApp(owner === 'org' ? org.trim() : '', shared))(); }}>
      <p class="muted">
        Agents reach GitHub through a GitHub App of your own. GitHub opens with it filled in: create it, choose the
        repositories agents may work on, and you're back here.
      </p>
      <label class="toggle"><input type="radio" name="owner" checked={owner === 'me'} onChange={() => setOwner('me')} /> On my account</label>
      <label class="toggle"><input type="radio" name="owner" checked={owner === 'org'} onChange={() => setOwner('org')} /> On an organization I administer</label>
      {owner === 'org' && <input placeholder="Organization" autoFocus spellcheck={false} value={org} onInput={e => setOrg(e.currentTarget.value)} />}
      <label class="toggle">
        <input type="checkbox" checked={shared} onChange={e => setShared(e.currentTarget.checked)} /> Let other accounts install it
      </label>
      <span class="hint">Leave this off unless one server works for several accounts or organizations.</span>
      <div><button class="btn primary" disabled={busy || (owner === 'org' && !org.trim())}>{busy ? 'Opening GitHub…' : 'Create the GitHub App'}</button></div>
    </form>
  );
}

/** First-run prompt: shown until the server can reach GitHub. */
export function GitHubSetup() {
  const [st] = useLoad<GitHubStatus>('/api/github');
  if (!st || st.account) return null;
  return (
    <section class="card setup">
      <h2>Connect GitHub</h2>
      <ConnectGitHub status={st} />
    </section>
  );
}
