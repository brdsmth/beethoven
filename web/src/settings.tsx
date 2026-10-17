import type { ComponentChildren } from 'preact';
import { useEffect, useState } from 'preact/hooks';
import { api, get, getMe, resetGitHub, type Credentials, type GitHubStatus, type Info, type Settings as S, type SettingsInfo } from './api';
import { EnvironmentEdit, Environments } from './environments';
import { ConnectGitHub } from './github';
import { MCPServers } from './mcp';
import { AccountPage, AuditPage, PeoplePage } from './people';
import { useMe } from './teams';
import { SecretsPage } from './secrets';
import { SlackPage } from './slack';
import { ConfirmButton, EffortSelect, Group, Head, ModelField, SaveBar, errText, fail, num, str, toast, useLoad } from './util';

const SECTIONS: [string, [string, string][]][] = [
  ['You', [['account', 'Account'], ['github', 'GitHub']]],
  ['Agent', [['general', 'General'], ['credentials', 'Credentials']]],
  ['Workspace', [['people', 'People'], ['teams', 'Teams'], ['environments', 'Projects'], ['secrets', 'Secrets'], ['mcp', 'MCP servers'],
    ['slack', 'Slack'], ['audit', 'Audit log']]],
  ['Server', [['about', 'About']]],
];

export function Settings({ info, route }: { info?: Info; route: string[] }) {
  const [section, id, tab] = route;
  const me = useMe();
  useEffect(() => { if (section === 'teams') location.replace('#/teams'); }, [section]);
  useEffect(() => { // wide screens open a section; narrow ones show the list first
    if (!section && !matchMedia('(max-width: 900px)').matches) location.replace('#/settings/general');
  }, [section]);
  const body = section === 'account' ? <AccountPage />
    : section === 'people' ? <PeoplePage />
    : section === 'audit' ? (me?.admin ? <AuditPage /> : <p class="empty">Only admins see the audit log. Team leads see their team's on its page.</p>)
    : section === 'general' ? <General />
    : section === 'credentials' ? <CredentialsPage />
    : section === 'github' ? <GitHubPage />
    : section === 'slack' ? <SlackPage />
    : section === 'environments' ? (id ? <EnvironmentEdit key={id} id={id} tab={tab} /> : <Environments />)
    : section === 'secrets' ? <SecretsPage scope={id || ''} />
    : section === 'mcp' ? <MCPServers />
    : section === 'about' ? <About info={info} />
    : section && <p class="empty">No such settings page.</p>;
  return (
    <div class={'settings' + (section ? ' has-section' : '')}>
      <nav class="settings-nav" aria-label="Settings">
        <div class="settings-title">Settings</div>
        {SECTIONS.map(([group, items]) => (
          <div key={group} class="nav-group">
            <h3>{group}</h3>
            {items.filter(([k]) => k !== 'audit' || me?.admin).map(([k, label]) =>
              <a key={k} href={k === 'teams' ? '#/teams' : `#/settings/${k}`} class={k === section ? 'active' : ''}>{label}</a>)}
          </div>
        ))}
      </nav>
      <div class="settings-body">
        {!(section === 'environments' && id) && <a class="back narrow-only" href="#/settings">‹ Settings</a>}
        {body}
      </div>
    </div>
  );
}

// ------------------------------------------------------------------ general

const TEXT = ['model', 'effort', 'instructions', 'image', 'branch_prefix', 'author_name', 'author_email'] as const;
const NUMS = ['cpus', 'memory_mb', 'idle_minutes', 'turn_minutes'] as const;
type GF = Record<(typeof TEXT)[number] | (typeof NUMS)[number], string> & { draft_prs: boolean };
const toGF = (s: S) => ({
  ...Object.fromEntries(TEXT.map(k => [k, s[k] || ''])), ...Object.fromEntries(NUMS.map(k => [k, str(s[k])])), draft_prs: !!s.draft_prs,
}) as GF;
const fromGF = (f: GF): S => ({
  ...Object.fromEntries(TEXT.map(k => [k, k === 'instructions' ? f[k] : f[k].trim()])),
  ...Object.fromEntries(NUMS.map(k => [k, num(f[k])])), draft_prs: f.draft_prs,
});

function General() {
  const [data, , setData] = useLoad<SettingsInfo>('/api/settings');
  const [f, setF] = useState<GF>();
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (data) setF(toGF(data.settings)); }, [data]);
  if (!data || !f) return <p class="empty">Loading…</p>;
  const d = data.defaults;
  const dirty = JSON.stringify(fromGF(f)) !== JSON.stringify(fromGF(toGF(data.settings)));
  const set = (k: keyof GF, v: string | boolean) => { setF({ ...f, [k]: v }); setErr(''); };
  const bind = (k: keyof GF) => ({ value: f[k] as string, onInput: (e: Event) => set(k, (e.currentTarget as HTMLInputElement).value) });
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    try {
      const settings = await api<S>('PUT', '/api/settings', fromGF(f));
      setData({ ...data, settings });
      toast('Settings saved');
    } catch (e) { setErr(errText(e)); }
    setBusy(false);
  };
  return (
    <form class="form" onSubmit={save}>
      <Head title="General">Defaults for every session. Environments can override the model, instructions and sandbox.</Head>
      <Group title="Model">
        <div class="grid2">
          <label>Default model<ModelField value={f.model} set={v => set('model', v)} def="Agent default" /></label>
          <label>Effort<EffortSelect value={f.effort} set={v => set('effort', v)} def="Agent default" /></label>
        </div>
        <label>Custom instructions
          <textarea rows={5} placeholder="e.g. Run the test suite before committing." {...bind('instructions')} />
          <span class="hint">Added to every agent's system prompt.</span>
        </label>
      </Group>
      <Group title="Pull requests">
        <label class="toggle"><input type="checkbox" checked={f.draft_prs} onChange={e => set('draft_prs', e.currentTarget.checked)} /> Open pull requests as drafts</label>
        <label>Branch prefix<input class="mono" placeholder={d.branch_prefix} spellcheck={false} {...bind('branch_prefix')} />
          <span class="hint">Work branches are named with this prefix.</span></label>
      </Group>
      <Group title="Commits" hint="The author of commits no person asked for: automations and the server's own token. People commit as themselves. Set both or neither.">
        <div class="grid2">
          <label>Author name<input placeholder={d.author_name || 'Name'} {...bind('author_name')} /></label>
          <label>Author email<input type="email" placeholder={d.author_email || 'Email'} {...bind('author_email')} /></label>
        </div>
      </Group>
      <Group title="Sandbox" hint="Image, CPUs and memory apply when a session's sandbox is first created.">
        <label>Image<input class="mono" placeholder={d.image} spellcheck={false} {...bind('image')} /></label>
        <div class="grid2">
          <label>CPUs<input type="number" min="0" max="64" step="any" placeholder="No limit" {...bind('cpus')} /></label>
          <label>Memory (MB)<input type="number" min="0" placeholder="No limit" {...bind('memory_mb')} /></label>
          <label>Stop idle sandboxes after (minutes)<input type="number" min="0" placeholder={String(d.idle_minutes)} {...bind('idle_minutes')} /></label>
          <label>Prompt time limit (minutes)<input type="number" min="0" placeholder={String(d.turn_minutes)} {...bind('turn_minutes')} /></label>
        </div>
      </Group>
      <SaveBar dirty={dirty} busy={busy} err={err} />
    </form>
  );
}

// -------------------------------------------------------------- credentials

const ANTHROPIC: Record<string, string> = {
  settings: 'Using the credential stored here.',
  environment: "Using a credential from the server's environment.",
  secret: 'Using a global secret.',
  '': "Not configured, so agents can't run.",
};
function CredentialsPage() {
  const [c, reload] = useLoad<Credentials>('/api/credentials');
  if (!c) return <p class="empty">Loading…</p>;
  const a = c.anthropic.source;
  return (
    <>
      <Head title="Credentials">
        Stored encrypted on this server; values are never shown again. A credential stored here takes precedence over the server's
        environment. GitHub is connected under <a href="#/settings/github">GitHub</a>.
      </Head>
      <section class="card">
        <div class="card-head"><h2>Anthropic</h2><Status ok={!!a} /></div>
        <p class="hint">{ANTHROPIC[a] ?? a} Agents need an API key or a subscription token; either works.</p>
        <CredRow name="ANTHROPIC_API_KEY" label="API key" stored={c.stored.ANTHROPIC_API_KEY} done={reload}
          hint="From the Anthropic Console. Billed per use." />
        <CredRow name="CLAUDE_CODE_OAUTH_TOKEN" label="Subscription token" stored={c.stored.CLAUDE_CODE_OAUTH_TOKEN} done={reload}
          hint={<>Uses a Claude subscription. Create one with <code>claude setup-token</code>.</>} />
      </section>
    </>
  );
}

// ------------------------------------------------------------------- github

const TOKEN: Record<string, string> = {
  settings: 'Using the token stored here.',
  environment: "Using GITHUB_TOKEN from the server's environment.",
  gh: "Using the GitHub CLI's login on the server.",
  '': 'No token is set.',
};

function GitHubPage() {
  const [st, reload] = useLoad<GitHubStatus>('/api/github');
  const [c, reloadCreds, setC] = useLoad<Credentials>('/api/credentials');
  const [checking, setChecking] = useState(false);
  if (!st || !c) return <p class="empty">Loading…</p>;
  const changed = () => { resetGitHub(); reload(); reloadCreds(); };
  const check = async () => {
    setChecking(true);
    try { setC(await get<Credentials>('/api/credentials?check=1')); } catch (e) { fail(e); }
    setChecking(false);
  };
  const { app, account } = st, g = c.github;
  const me = getMe();
  return (
    <>
      <Head title="GitHub">
        Agents clone, push and open pull requests through a GitHub App, as the person who started the session: everyone
        connects their own GitHub account. Each push uses a token for that one repository, valid for an hour.
      </Head>
      {st.error && <p class="err" role="alert">{st.error}</p>}
      <section class="card">
        <div class="card-head">
          <h2>Your GitHub account</h2><span class={'chip ' + (account ? 'green' : 'amber')}>{account ? 'Connected' : 'Not connected'}</span>
          {account && <span class="push"><ConfirmButton small label="Disconnect" note="Your sessions can't reach GitHub until you connect again."
            onConfirm={() => api('DELETE', '/api/github/account').then(changed)} /></span>}
        </div>
        {account
          ? <p>Connected as <b>@{account.login}</b>{account.name && <span class="muted"> ({account.name})</span>}. Your sessions' commits and pull
            requests are yours.</p>
          : <ConnectGitHub status={st} />}
      </section>
      {app && (
        <section class="card">
          <div class="card-head">
            <h2>App</h2><span class="chip">{app.source === 'environment' ? 'Preconfigured' : 'Created from this server'}</span>
            {app.source === 'settings' && me?.admin && <span class="push"><ConfirmButton small label="Remove" ask="Remove app"
              note="Disconnects everyone. The app stays on GitHub; delete it there."
              onConfirm={() => api('DELETE', '/api/github/app').then(changed)} /></span>}
          </div>
          <p><a href={app.url} target="_blank" rel="noreferrer">{app.name || app.slug}</a> <span class="muted">owned by @{app.owner}</span></p>
          {account && (
            <>
              <div class="rows">
                {st.installations.map(i => (
                  <a key={i.id} class="row link" href={i.url} target="_blank" rel="noreferrer">
                    <span class="row-main">
                      <span class="row-title">{i.account}</span>
                      <span class="row-desc">{i.selection === 'all' ? 'All repositories' : 'Selected repositories'}</span>
                    </span>
                    <span class="hint">Manage ›</span>
                  </a>
                ))}
              </div>
              {!st.installations.length && <p class="warn">Not installed on any repositories yet.</p>}
              <div><a class="btn" href={app.install_url}>Add repositories or accounts</a></div>
            </>
          )}
        </section>
      )}
      {(me?.admin || !me?.people) && <section class="card">
        <div class="card-head">
          <h2>Personal access token</h2>
          {g.source && <button class="btn sm push" disabled={checking} onClick={check}>{checking ? 'Checking…' : 'Check'}</button>}
        </div>
        <p class="hint">
          Instead of the app: shared by everyone until anyone connects the app. {TOKEN[st.token] ?? st.token}
        </p>
        {g.login && <p class="ok">Belongs to <b>@{g.login}</b></p>}
        {g.error && <p class="err">{g.error}</p>}
        <CredRow name="GITHUB_TOKEN" label="Access token" stored={c.stored.GITHUB_TOKEN} done={changed}
          hint="Needs read and write access to contents and pull requests of the repositories agents work on." />
      </section>}
    </>
  );
}

const Status = ({ ok }: { ok: boolean }) => <span class={'chip ' + (ok ? 'green' : 'amber')}>{ok ? 'Configured' : 'Missing'}</span>;

function CredRow({ name, label, hint, stored, done }: {
  name: string; label: string; hint: ComponentChildren; stored: boolean; done: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [v, setV] = useState('');
  const [err, setErr] = useState('');
  const put = async (value: string) => {
    await api('PUT', '/api/credentials', { name, value });
    setEditing(false);
    setV('');
    toast(value ? `${label} saved` : `${label} removed`);
    done();
  };
  return (
    <div class="cred">
      <div class="cred-main">
        <div><b>{label}</b> <span class="mono dim">{name}</span></div>
        <div class="hint">{hint}</div>
      </div>
      {!editing && (
        <div class="actions">
          <span class={'chip' + (stored ? ' green' : '')}>{stored ? 'Stored' : 'Not set'}</span>
          <button class="btn sm" onClick={() => setEditing(true)}>{stored ? 'Replace' : 'Set'}</button>
          {stored && <ConfirmButton small label="Remove" ask="Remove" onConfirm={() => put('')} />}
        </div>
      )}
      {editing && (
        <form class="inline-form wide" onSubmit={e => { e.preventDefault(); put(v.trim()).catch(e => setErr(errText(e))); }}>
          <input type="password" autoFocus autocomplete="off" aria-label={label} placeholder={`Paste the ${label.toLowerCase()}`}
            value={v} onInput={e => { setV(e.currentTarget.value); setErr(''); }} />
          <button type="button" class="btn" onClick={() => { setEditing(false); setV(''); setErr(''); }}>Cancel</button>
          <button class="btn primary" disabled={!v.trim()}>Save</button>
          {err && <span class="err">{err}</span>}
        </form>
      )}
    </div>
  );
}

// -------------------------------------------------------------------- about

function About({ info }: { info?: Info }) {
  if (!info) return <p class="empty">Loading…</p>;
  return (
    <>
      <Head title="About" />
      <dl class="kv">
        <dt>Version</dt><dd class="mono">{info.version}</dd>
        <dt>Commit author</dt>
        <dd>{info.author.name} {info.author.email && <span class="dim">&lt;{info.author.email}&gt;</span>} · <a href="#/settings/general">change</a></dd>
        <dt>Default model</dt><dd class="mono">{info.model || 'agent default'}</dd>
        <dt>Default effort</dt><dd class="mono">{info.effort || 'agent default'}</dd>
        <dt>Anthropic</dt>
        <dd>{info.anthropic ? <span class="ok">Credential configured</span> : <span class="warn">Missing</span>} · <a href="#/settings/credentials">credentials</a></dd>
        <dt>GitHub</dt>
        <dd>{info.github ? <span class="ok">Connected</span> : <span class="warn">Not connected: sessions can't push or open pull requests</span>} · <a href="#/settings/github">GitHub</a></dd>
      </dl>
    </>
  );
}
