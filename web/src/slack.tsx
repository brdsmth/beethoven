import { useEffect, useState } from 'preact/hooks';
import { api, type Environment, type SettingsInfo, type SlackStatus } from './api';
import { useMe, useTeams } from './teams';
import { ConfirmButton, Head, Spinner, ago, errText, fail, toast, useLoad } from './util';

// Connecting Slack: create the server's Slack app from a configuration
// token (unless one is preconfigured), then add it to a workspace. Slack
// returns the browser to /?slack=installed, and SlackReturn completes it.

const SETTINGS = '/#/settings/slack';

/** Slack only reaches public HTTPS addresses. */
const reachable = () => location.protocol === 'https:' && !/^(localhost|127\.|10\.|192\.168\.|\[::1\])/.test(location.hostname);

async function addToSlack() {
  const r = await api<{ url: string }>('POST', '/api/slack/install', { base_url: location.origin });
  location.assign(r.url);
}

/** The step Slack returned the browser from, if it did. */
export const slackStep = () => new URLSearchParams(location.search).get('slack');

export function SlackReturn() {
  const [err, setErr] = useState('');
  useEffect(() => {
    const q = new URLSearchParams(location.search);
    (async () => {
      if (q.get('error')) throw new Error(q.get('error') === 'access_denied' ? 'Slack installation was cancelled.' : q.get('error')!);
      await api('POST', '/api/slack/installed', { code: q.get('code') || '', state: q.get('state') || '' });
      location.replace(SETTINGS);
    })().catch(e => setErr(errText(e)));
  }, []);
  return (
    <div class="page narrow">
      <h1>Connecting Slack</h1>
      {err ? <><p class="err" role="alert">{err}</p><p><a href={SETTINGS}>Back to Slack settings</a></p></> : <p class="muted"><Spinner /> One moment…</p>}
    </div>
  );
}

function CreateApp() {
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const create = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api<{ next: string }>('POST', '/api/slack/app', { config_token: token.trim(), base_url: location.origin });
      location.assign(r.next); // on to adding it to the workspace
    } catch (e) { fail(e); setBusy(false); }
  };
  return (
    <form class="stack" onSubmit={create}>
      <p class="muted">Beethoven creates its Slack app for you; Slack needs one token from you to allow it.</p>
      <ol class="steps">
        <li>
          Open <a href="https://api.slack.com/apps" target="_blank" rel="noreferrer">your Slack apps</a>, find <b>Your App Configuration
          Tokens</b> at the bottom, click <b>Generate Token</b>, choose your workspace and copy the <b>access token</b>.
        </li>
        <li>Paste it here. It's used once to create the app and isn't stored.</li>
      </ol>
      <input type="password" autocomplete="off" placeholder="xoxe.xoxp-…" aria-label="App configuration token" value={token}
        onInput={e => setToken(e.currentTarget.value)} />
      <div><button class="btn primary" disabled={busy || !token.trim() || !reachable()}>{busy ? 'Creating…' : 'Create the Slack app'}</button></div>
    </form>
  );
}

export function SlackPage({ cloud }: { cloud?: boolean }) {
  const [st, reload] = useLoad<SlackStatus>('/api/slack');
  const [envs] = useLoad<Environment[]>('/api/environments');
  const [settings, reloadSettings] = useLoad<SettingsInfo>('/api/settings');
  const [busy, setBusy] = useState(false);
  const teams = useTeams();
  const me = useMe();
  if (!st || !envs) return <p class="empty">Loading…</p>;
  const bind = (c: SlackStatus['channels'][number], team: string) =>
    api('PUT', `/api/slack/channels/${c.team_id}/${c.channel}/team`, { owner_team_id: team }).then(reload, fail);
  const membersOnly = async (on: boolean) => {
    try {
      await api('PUT', '/api/settings', { ...settings!.settings, slack_members_only: on });
      reloadSettings();
      toast(on ? 'Only people with an account can ask now' : 'Anyone in the workspace can ask now');
    } catch (e) { fail(e); }
  };
  const add = async () => { setBusy(true); try { await addToSlack(); } catch (e) { fail(e); setBusy(false); } };
  const target = (c: SlackStatus['channels'][number]) => c.environment_id
    ? `${envs.find(e => e.id === c.environment_id)?.name ?? 'a deleted project'} (project)`
    : c.repos.length ? c.repos.map(r => r.repo).join(', ') : 'No repository';
  return (
    <>
      <Head title="Slack">
        Mention <b>@beethoven</b> in a channel, or send it a direct message, and it works on the request in a session and answers
        in the thread. Mention it again in the thread to follow up.
      </Head>
      {!cloud && !reachable() && (
        <p class="banner warn">
          Slack can only reach Beethoven at a public HTTPS address. Open Beethoven through that address to connect Slack; for local
          testing, a tunnel such as <code>cloudflared tunnel --url http://localhost:7070</code> works, with its host in BEETHOVEN_HOSTS.
        </p>
      )}
      <section class="card">
        <div class="card-head">
          <h2>Workspaces</h2>
          <span class={'chip ' + (st.installations.length ? 'green' : 'amber')}>{st.installations.length ? 'Connected' : 'Not connected'}</span>
        </div>
        {!st.app ? (cloud ? <p class="muted">Slack isn't available on this service yet.</p> : <CreateApp />) : (
          <>
            <div class="rows">
              {st.installations.map(i => (
                <div key={i.team_id} class="row">
                  <span class="row-main">
                    <span class="row-title">{i.team_name || i.team_id}</span>
                    <span class="row-desc">Added {ago(i.created_at)}</span>
                  </span>
                  <ConfirmButton small label="Remove" note="The bot leaves this workspace."
                    onConfirm={() => api('DELETE', `/api/slack/installations/${i.team_id}`).then(reload)} />
                </div>
              ))}
            </div>
            <div><button class="btn primary" disabled={busy || !reachable()} onClick={add}>
              {busy ? 'Opening Slack…' : st.installations.length ? 'Add to another workspace' : 'Add to Slack'}
            </button></div>
          </>
        )}
      </section>
      {st.app && (
        <section class="card">
          <div class="card-head">
            <h2>App</h2><span class="chip">{st.app.source === 'environment' ? 'Preconfigured' : 'Created from this server'}</span>
            {st.app.source === 'settings' && <span class="push"><ConfirmButton small label="Remove" ask="Remove app"
              note="Disconnects every workspace. The app stays on Slack; delete it there."
              onConfirm={() => api('DELETE', '/api/slack/app').then(reload)} /></span>}
          </div>
          <p><a href={st.app.settings_url} target="_blank" rel="noreferrer">Slack app settings</a> <span class="muted mono">{st.app.app_id}</span></p>
        </section>
      )}
      {st.app && me?.people && settings && (
        <section class="card">
          <div class="card-head"><h2>Who can ask</h2></div>
          <label class="toggle">
            <input type="checkbox" checked={!!settings.settings.slack_members_only} disabled={!me.admin}
              onChange={e => membersOnly(e.currentTarget.checked)} />
            Only people with a Beethoven account
          </label>
          <p class="hint">
            People are matched by their Slack email. Off, anyone in the Slack workspace can ask: someone without an account here
            works as the server's owner, with the owner's GitHub. On, they're asked to get an invitation.
          </p>
        </section>
      )}
      {st.channels.length > 0 && (
        <section class="card">
          <div class="card-head"><h2>Channels</h2></div>
          <p class="hint">
            What a request works on when it names no repository or project, set when someone answers the bot's question; and the
            team its sessions belong to. Only a team's members may ask in its channels.
          </p>
          <div class="rows">
            {st.channels.map(c => (
              <div key={c.team_id + c.channel} class="row">
                <span class="row-main">
                  <span class="row-title">{c.name || c.channel}</span>
                  <span class="row-desc">{target(c)}</span>
                </span>
                {!!teams?.length && (
                  <select aria-label="Team" value={c.owner_team_id || ''} onChange={e => bind(c, e.currentTarget.value)}>
                    <option value="">Workspace</option>
                    {teams.filter(t => !t.archived_at || t.id === c.owner_team_id).map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                  </select>
                )}
                <ConfirmButton small label="Forget" onConfirm={() => api('DELETE', `/api/slack/channels/${c.team_id}/${c.channel}`).then(reload)} />
              </div>
            ))}
          </div>
        </section>
      )}
    </>
  );
}
