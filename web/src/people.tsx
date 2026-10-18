import { useEffect, useState } from 'preact/hooks';
import { api, get, setMe, type APIToken, type AuditEvent, type AuthState, type Invite, type Person, type Role, type Team } from './api';
import { TeamMark, teamHref, useMe, useTeams } from './teams';
import { ConfirmButton, Group, Head, ago, errText, fail, toast, title, useLoad, when } from './util';

// People: owners and admins invite people and set their roles; everyone
// manages their own profile and API tokens.

const ROLES: [Role, string][] = [['member', 'Member'], ['admin', 'Admin'], ['owner', 'Owner']];
const ROLE_HINT = 'Admins manage people, teams, settings and integrations. Owners also make and unmake owners.';

/** An invitation link for a token. */
const inviteURL = (token: string) => `${location.origin}/#/invite/${token}`;

export function PeoplePage() {
  const me = useMe();
  const [people, reload] = useLoad<Person[]>('/api/people');
  const [invites, reloadInvites] = useLoad<Invite[]>(me?.admin ? '/api/invites' : false);
  if (!people || !me) return <p class="empty">Loading…</p>;
  const update = (p: Person, v: Partial<Person>) =>
    api<Person>('PUT', `/api/people/${p.id}`, { role: v.role ?? p.role, disabled: v.disabled ?? !!p.disabled }).then(reload);
  const mayChange = (p: Person) => me.admin && (p.role !== 'owner' || me.account?.role === 'owner');
  return (
    <>
      <Head title="People">Everyone with an account here. {ROLE_HINT}</Head>
      {!me.people && <p class="banner warn">Nobody signs in to this server: it's open to whoever reaches it. People and invitations apply once it asks people to sign in.</p>}
      <div class="rows">
        {people.map(p => (
          <div key={p.id} class={'row' + (p.disabled ? ' off' : '')}>
            <span class="row-main">
              <span class="row-title">{p.name || p.email}{p.id === me.account?.id && <span class="dim"> (you)</span>}</span>
              <span class="row-meta">
                {p.email && p.name && <span>{p.email}</span>}
                {p.github ? <span class="mono">@{p.github}</span> : <span class="dim">GitHub not connected</span>}
                {p.disabled && <span class="warn">deactivated</span>}
              </span>
            </span>
            {mayChange(p) ? (
              <select aria-label="Role" value={p.role} disabled={p.disabled}
                onChange={e => update(p, { role: e.currentTarget.value as Role }).catch(fail)}>
                {ROLES.filter(([r]) => r !== 'owner' || me.account?.role === 'owner' || p.role === 'owner').map(([r, l]) => <option key={r} value={r}>{l}</option>)}
              </select>
            ) : <span class="badge plain">{ROLES.find(([r]) => r === p.role)?.[1]}</span>}
            {mayChange(p) && p.id !== me.account?.id && (p.disabled
              ? <button class="btn sm" onClick={() => update(p, { disabled: false }).catch(fail)}>Reactivate</button>
              : <ConfirmButton small label="Deactivate" ask="Deactivate" note="Signs them out; their history stays."
                  onConfirm={() => update(p, { disabled: true })} />)}
          </div>
        ))}
      </div>
      {me.admin && me.people && <Invites invites={invites} changed={reloadInvites} />}
    </>
  );
}

function Invites({ invites, changed }: { invites?: Invite[]; changed: () => void }) {
  const me = useMe();
  const teams = useTeams();
  const [role, setRole] = useState<Role>('member');
  const [email, setEmail] = useState('');
  const [team, setTeam] = useState('');
  const [link, setLink] = useState('');
  const [err, setErr] = useState('');
  const create = async (e: Event) => {
    e.preventDefault();
    setErr('');
    try {
      const r = await api<{ token: string }>('POST', '/api/invites', { role, email: email.trim(), team_id: team });
      setLink(inviteURL(r.token));
      setEmail('');
      changed();
    } catch (e) { setErr(errText(e)); }
  };
  const copy = () => navigator.clipboard.writeText(link).then(() => toast('Link copied'), () => {});
  return (
    <Group title="Invite someone" hint="An invitation is a link that works once, for 7 days. Send it however you like: Beethoven doesn't send email.">
      <form class="form" onSubmit={create}>
        <div class="grid2">
          <label>Email<input type="email" placeholder="Optional: only they can use it" value={email} onInput={e => setEmail(e.currentTarget.value)} /></label>
          <label>Role
            <select value={role} onChange={e => setRole(e.currentTarget.value as Role)}>
              {ROLES.filter(([r]) => r !== 'owner' || me?.account?.role === 'owner').map(([r, l]) => <option key={r} value={r}>{l}</option>)}
            </select>
          </label>
        </div>
        {!!teams?.length && (
          <label>Team<select value={team} onChange={e => setTeam(e.currentTarget.value)}>
            <option value="">None</option>
            {teams.filter(t => !t.archived_at).map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select><span class="hint">They join it as a member.</span></label>
        )}
        <div class="form-foot">
          {err ? <span class="err" role="alert">{err}</span> : <span />}
          <button class="btn primary">Create invitation link</button>
        </div>
      </form>
      {link && (
        <div class="notice">
          <div class="copy-row">
            <input class="mono" readOnly value={link} onFocus={e => e.currentTarget.select()} aria-label="Invitation link" />
            <button class="btn" onClick={copy}>Copy</button>
          </div>
          <span class="hint">Shown once. Anyone with it can create the account{email ? '' : ', so send it privately'}.</span>
        </div>
      )}
      {!!invites?.length && (
        <div class="rows">
          {invites.map(i => (
            <div key={i.id} class="row">
              <span class="row-main">
                <span class="row-title">{i.email || 'Anyone with the link'}</span>
                <span class="row-meta"><span>{ROLES.find(([r]) => r === i.role)?.[1]}</span>{i.team && <span>joins {i.team}</span>}<span>expires {when(i.expires_at)}</span></span>
              </span>
              <ConfirmButton small label="Withdraw" ask="Withdraw" onConfirm={() => api('DELETE', `/api/invites/${i.id}`).then(changed)} />
            </div>
          ))}
        </div>
      )}
    </Group>
  );
}

// ------------------------------------------------------------------ account

export function AccountPage() {
  const me = useMe();
  const teams = useTeams();
  const [name, setName] = useState('');
  useEffect(() => { setName(me?.account?.name || ''); }, [me?.account?.name]);
  if (!me) return <p class="empty">Loading…</p>;
  if (!me.account) {
    return (
      <>
        <Head title="Your account" />
        <p class="muted">Nobody signs in to this server, so there are no accounts: everyone who reaches it acts as the server's owner.</p>
      </>
    );
  }
  const save = (v: Record<string, string>) => api<AuthState>('PUT', '/api/me', v).then(a => { setMe(a); toast('Saved'); }, fail);
  const mine = teams?.filter(t => t.role && !t.archived_at) || [];
  return (
    <>
      <Head title="Your account">{me.account.email} · {ROLES.find(([r]) => r === me.account!.role)?.[1]}</Head>
      <Group title="Profile">
        <form class="inline-form" onSubmit={e => { e.preventDefault(); save({ name: name.trim() }); }}>
          <input aria-label="Name" placeholder="Your name" maxLength={100} value={name} onInput={e => setName(e.currentTarget.value)} />
          <button class="btn" disabled={name.trim() === me.account.name}>Save</button>
        </form>
        <p class="hint">Your commits are authored by your GitHub account once you <a href="#/settings/github">connect it</a>; until then, by this name and email.</p>
      </Group>
      {mine.length > 0 && (
        <Group title="Default team" hint="Where your Slack direct messages go, and the team new sessions start in.">
          <select aria-label="Default team" value={me.default_team_id} onChange={e => save({ default_team_id: e.currentTarget.value })}>
            <option value="">None: the workspace</option>
            {mine.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        </Group>
      )}
      <Group title="Your teams">
        {mine.length === 0 && <p class="empty small">You're not in a team. <a href={teamHref()}>Browse teams</a></p>}
        <div class="rows">
          {mine.map((t: Team) => (
            <a key={t.id} class="row link" href={teamHref(t.slug)}>
              <TeamMark team={t} />
              <span class="row-main"><span class="row-title">{t.name}</span></span>
              <span class="badge plain">{t.role === 'lead' ? 'Lead' : 'Member'}</span>
            </a>
          ))}
        </div>
      </Group>
      <Tokens />
    </>
  );
}

function Tokens() {
  const [list, reload] = useLoad<APIToken[]>('/api/tokens');
  const [name, setName] = useState('');
  const [made, setMade] = useState('');
  const create = async (e: Event) => {
    e.preventDefault();
    try {
      const r = await api<{ value: string }>('POST', '/api/tokens', { name: name.trim() });
      setMade(r.value);
      setName('');
      reload();
    } catch (e) { fail(e); }
  };
  return (
    <Group title="API tokens" hint={<>For scripts and the CLI: <code>BEETHOVEN_TOKEN=bt_…</code>. A token acts as you, with your teams and your GitHub.</>}>
      <form class="inline-form" onSubmit={create}>
        <input aria-label="Token name" placeholder="What it's for, e.g. laptop CLI" maxLength={60} value={name} onInput={e => setName(e.currentTarget.value)} />
        <button class="btn" disabled={!name.trim()}>Create token</button>
      </form>
      {made && (
        <div class="notice">
          <div class="copy-row">
            <input class="mono" readOnly value={made} onFocus={e => e.currentTarget.select()} aria-label="New token" />
            <button class="btn" onClick={() => navigator.clipboard.writeText(made).then(() => toast('Token copied'), () => {})}>Copy</button>
          </div>
          <span class="hint">Shown once: copy it now.</span>
        </div>
      )}
      <div class="rows">
        {list?.map(t => (
          <div key={t.id} class="row">
            <span class="row-main">
              <span class="row-title">{t.name}</span>
              <span class="row-meta"><span class="mono">bt_…{t.hint}</span><span>made {ago(t.created_at)}</span>
                <span>{t.last_used_at ? 'used ' + ago(t.last_used_at) : 'never used'}</span></span>
            </span>
            <ConfirmButton small label="Revoke" ask="Revoke" onConfirm={() => api('DELETE', `/api/tokens/${t.id}`).then(reload)} />
          </div>
        ))}
      </div>
    </Group>
  );
}

// -------------------------------------------------------------------- audit

const ACTIONS: Record<string, string> = {
  'account.created': 'joined', 'account.updated': 'changed a person', 'invite.created': 'invited someone',
  'invite.withdrawn': 'withdrew an invitation', 'token.created': 'made an API token', 'token.revoked': 'revoked an API token',
  'team.created': 'created a team', 'team.updated': 'changed a team', 'team.archived': 'archived a team', 'team.restored': 'restored a team',
  'team.member_added': 'added someone to a team', 'team.member_role_changed': "changed someone's team role",
  'team.member_removed': 'removed someone from a team', 'team.member_left': 'left a team', 'team.member_joined': 'joined a team',
  'secret.set': 'set a secret', 'secret.deleted': 'deleted a secret', 'session.visibility_changed': 'changed who sees a session',
  'slack.channel_bound': "changed a Slack channel's team", 'slack.channel_cleared': "cleared a Slack channel's defaults",
};

export function AuditPage({ team }: { team?: string }) {
  const [list, setList] = useState<AuditEvent[]>();
  const [more, setMore] = useState(false);
  const teams = useTeams();
  const load = (before = 0) => get<AuditEvent[]>(`/api/audit?team=${team || ''}&before=${before}`).then(l => {
    setList(prev => before ? [...(prev || []), ...l] : l);
    setMore(l.length === 100);
  }, fail);
  useEffect(() => { load(); }, [team]);
  const teamOf = (id?: string) => teams?.find(t => t.id === id);
  return (
    <>
      <Head title="Audit log">Changes to who may do what: people, teams, secrets (names only), channels and session visibility.</Head>
      {list?.length === 0 && <p class="empty">Nothing yet.</p>}
      <div class="rows audit">
        {list?.map(e => (
          <div key={e.id} class="row">
            <span class="row-main">
              <span class="row-title"><b>{e.actor || 'The server'}</b> {ACTIONS[e.action] || e.action}{detailText(e)}</span>
              <span class="row-meta">
                <span>{when(e.at)}</span>
                {e.team_id && <span><TeamMark team={teamOf(e.team_id)} size={12} /> {teamOf(e.team_id)?.name || 'a team'}</span>}
                <span class="mono dim">{e.action}</span>
              </span>
            </span>
          </div>
        ))}
      </div>
      {more && <button class="btn" onClick={() => load(list![list!.length - 1].id)}>Older</button>}
    </>
  );
}

function detailText(e: AuditEvent): string {
  const d = e.detail as Record<string, any>;
  if (e.action.startsWith('secret.')) return `: ${e.target} in ${d.scope || 'global'}`;
  if (e.action === 'account.updated' && Array.isArray(d.role)) {
    const parts = [d.role[0] !== d.role[1] && `${d.email}: ${d.role[0]} → ${d.role[1]}`, d.disabled?.[1] && !d.disabled?.[0] && `${d.email} deactivated`,
      !d.disabled?.[1] && d.disabled?.[0] && `${d.email} reactivated`].filter(Boolean);
    return parts.length ? ': ' + parts.join(', ') : '';
  }
  if (d.person) return `: ${d.person}${d.role ? ` (${d.role})` : ''}`;
  if (d.name) return `: ${d.name}`;
  if (d.title) return `: ${d.title}${d.to ? ` → ${d.to}` : ''}`;
  if (d.email) return `: ${d.email}`;
  return '';
}

// ---------------------------------------------------------------- invitation

/** The page an invitation link opens: create the account, then sign in. */
export function InviteAccept({ token }: { token: string }) {
  const [info, setInfo] = useState<{ role: Role; email?: string; from?: string; team?: string }>();
  const [missing, setMissing] = useState('');
  const [f, setF] = useState({ name: '', email: '', password: '' });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  useEffect(() => {
    get<typeof info>(`/api/auth/invite?token=${encodeURIComponent(token)}`).then(i => {
      setInfo(i);
      if (i?.email) setF(p => ({ ...p, email: i.email! }));
    }, e => setMissing(errText(e)));
  }, [token]);
  const field = (k: keyof typeof f) => (e: Event) => setF({ ...f, [k]: (e.currentTarget as HTMLInputElement).value });
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setErr('');
    try {
      await api('POST', '/api/auth/invite', { token, ...f });
      location.replace('/#/');
      location.reload();
    } catch (e) { setErr(errText(e) + '.'); setBusy(false); }
  };
  title('Join');
  return (
    <div class="auth">
      <form class="auth-card" onSubmit={submit}>
        <div class="brand">Beethoven</div>
        {missing ? (
          <div class="auth-head"><h1>Invitation</h1><p class="auth-error" role="alert">{missing}.</p><a href="/#/">Sign in</a></div>
        ) : !info ? <p class="muted">One moment…</p> : (
          <>
            <div class="auth-head">
              <h1>Join Beethoven</h1>
              <p class="muted">{info.from ? `${info.from} invited you` : "You're invited"} as {info.role === 'member' ? 'a member' : 'an ' + info.role}
                {info.team ? `, in ${info.team}` : ''}. Create your account.</p>
            </div>
            <label class="field"><span class="field-label">Name</span>
              <input autoFocus autoComplete="name" value={f.name} onInput={field('name')} /></label>
            <label class="field"><span class="field-label">Email</span>
              <input type="email" required readOnly={!!info.email} autoComplete="username" value={f.email} onInput={field('email')} /></label>
            <label class="field"><span class="field-label">Password</span>
              <input type="password" required minLength={8} autoComplete="new-password" value={f.password} onInput={field('password')} />
              <span class="hint">At least 8 characters.</span></label>
            {err && <p class="auth-error" role="alert">{err}</p>}
            <button class="btn primary block" disabled={busy}>{busy ? 'Creating…' : 'Create account'}</button>
          </>
        )}
      </form>
    </div>
  );
}
