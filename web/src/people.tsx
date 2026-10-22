import { useEffect, useState } from 'preact/hooks';
import { api, get, setMe, type APIToken, type AuditEvent, type AuthState, type Environment, type Invite, type Person, type Role, type Team } from './api';
import { TeamMark, teamHref, useMe, useTeams } from './teams';
import { ConfirmButton, Group, Head, Seg, ago, errText, fail, toast, useLoad, when } from './util';

// People: owners and admins invite people and set their roles; everyone
// manages their own profile and API tokens.

const ROLES: [Role, string][] = [['member', 'Member'], ['admin', 'Admin'], ['owner', 'Owner']];
const ROLE_HINT = 'Admins manage people, teams, settings and integrations. Owners also make and unmake owners.';

/** An invitation link for a token. */
const inviteURL = (token: string) => `${location.origin}/#/invite/${token}`;

/** The names of the server's owners and admins, to say whom to ask; loaded while on. */
export function useAdmins(on = true) {
  const [people] = useLoad<Person[]>(on ? '/api/people' : false);
  return people?.filter(p => !p.disabled && (p.role === 'owner' || p.role === 'admin')).map(p => p.name || p.email || 'an admin');
}

/** "Ada", "Ada or Ken", "Ada, Ken or Lin": whom to ask. */
export const adminNames = (names?: string[]) => {
  const n = (names || []).filter(Boolean).slice(0, 3);
  if (!n.length) return 'an admin';
  return n.length === 1 ? n[0] : n.slice(0, -1).join(', ') + ' or ' + n[n.length - 1];
};

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

/** The message to send with an invitation: what Beethoven is, the link, and what comes first. */
function inviteMessage(v: { link: string; expires: string; team?: Team; empty: boolean; prompt: string; note: string; from: string }) {
  const lines = [
    `Hi, I've invited you to Beethoven, where we hand coding tasks to agents that open pull requests for us.${v.team ? ` You'll be in ${v.team.name}.` : ''}`,
    '', `Join here (it works once, until ${new Date(v.expires).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}):`, v.link,
  ];
  if (v.prompt) lines.push('', "I've left you a first task to try.");
  else if (v.team && v.empty) lines.push('', `${v.team.name} is new, so first you'll connect its repositories. Beethoven walks you through it.`);
  if (v.note) lines.push('', v.note);
  if (v.from) lines.push(`– ${v.from}`);
  return lines.join('\n');
}

function Invites({ invites, changed }: { invites?: Invite[]; changed: () => void }) {
  const me = useMe();
  const teams = useTeams();
  const [envs] = useLoad<Environment[]>('/api/environments');
  const [role, setRole] = useState<Role>('member');
  const [email, setEmail] = useState('');
  const [team, setTeam] = useState('');
  const [note, setNote] = useState('');
  const [prompt, setPrompt] = useState('');
  const [env, setEnv] = useState('');
  const [made, setMade] = useState<{ link: string; message: string; email: string }>();
  const [show, setShow] = useState<'message' | 'link'>('message');
  const [err, setErr] = useState('');
  const teamObj = teams?.find(t => t.id === team);
  const projects = envs?.filter(e => (team ? e.team_id === team : !e.team_id));
  const project = projects?.find(e => e.id === env) || projects?.find(e => e.id === teamObj?.default_environment_id) || projects?.[0];
  const empty = projects?.length === 0;
  const create = async (e: Event) => {
    e.preventDefault();
    setErr('');
    try {
      const first = empty ? '' : prompt.trim();
      const r = await api<{ token: string; invite: Invite }>('POST', '/api/invites', {
        role, email: email.trim(), team_id: team, note: note.trim(), first_prompt: first, environment_id: first && project ? project.id : '',
      });
      const link = inviteURL(r.token);
      setMade({ link, email: email.trim(), message: inviteMessage({ link, expires: r.invite.expires_at, team: teamObj, empty: !!empty, prompt: first,
        note: note.trim(), from: me?.account?.name || '' }) });
      setEmail('');
      setNote('');
      setPrompt('');
      changed();
    } catch (e) { setErr(errText(e)); }
  };
  const copy = () => navigator.clipboard.writeText(show === 'link' ? made!.link : made!.message).then(() => toast(show === 'link' ? 'Link copied' : 'Message copied'), () => {});
  return (
    <Group title="Invite someone" hint="An invitation is a link that works once, for 7 days. Beethoven doesn't send email: copy the message it writes into Slack or an email.">
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
          <label>Team<select value={team} onChange={e => { setTeam(e.currentTarget.value); setEnv(''); }}>
            <option value="">None</option>
            {teams.filter(t => !t.archived_at).map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select><span class="hint">They join it as a member. Someone who already has an account can use the link to join it too.</span></label>
        )}
        {projects && (empty ? (
          <p class="team-state empty">
            {teamObj ? <TeamMark team={teamObj} /> : null}
            <span>{teamObj ? teamObj.name : 'The workspace'} has no projects yet. They'll be asked to connect its repositories or start a new one,
              then get a first session for it. <a href={'#/settings/environments/new' + (team ? '?team=' + team : '')}>Set one up now</a> to skip that.</span>
          </p>
        ) : (
          <>
            <p class="team-state ok">
              {teamObj ? <TeamMark team={teamObj} /> : null}
              <span>{teamObj ? teamObj.name : 'The workspace'} has {projects.length === 1 ? '1 project' : `${projects.length} projects`}.
                {project ? ` They'll start in ${project.name}.` : ''}</span>
            </p>
            <label>A first session to suggest
              <textarea rows={3} maxLength={4000} placeholder="Optional: a small task to try first, e.g. fix a flaky test. It's waiting in their prompt."
                value={prompt} onInput={e => setPrompt(e.currentTarget.value)} />
            </label>
            {prompt.trim() && projects.length > 1 && (
              <label>In the project<select value={project?.id} onChange={e => setEnv(e.currentTarget.value)}>
                {projects.map(e => <option key={e.id} value={e.id}>{e.name}</option>)}
              </select></label>
            )}
          </>
        ))}
        <label>A note for them
          <textarea rows={2} maxLength={280} placeholder="Optional: shown on the invitation" value={note} onInput={e => setNote(e.currentTarget.value)} />
        </label>
        <div class="form-foot">
          {err ? <span class="err" role="alert">{err}</span> : <span />}
          <button class="btn primary">Create invitation</button>
        </div>
      </form>
      {made && (
        <div class="notice invite-made">
          <div class="invite-made-head">
            <b>Ready to send</b>
            <Seg value={show} set={setShow} options={[['message', 'Message'], ['link', 'Link only']]} />
          </div>
          {show === 'message' ? <textarea readOnly rows={8} value={made.message} aria-label="Invitation message" onFocus={e => e.currentTarget.select()} />
            : <input class="mono" readOnly value={made.link} onFocus={e => e.currentTarget.select()} aria-label="Invitation link" />}
          <div class="copy-row">
            <span class="hint">Shown once. {made.email ? `Only ${made.email} can use it.` : 'Anyone with the link can use it, so send it privately.'}</span>
            <button type="button" class="btn primary" onClick={copy}>{show === 'link' ? 'Copy link' : 'Copy message'}</button>
          </div>
        </div>
      )}
      {!!invites?.length && (
        <div class="rows">
          {invites.map(i => (
            <div key={i.id} class="row">
              <span class="row-main">
                <span class="row-title">{i.email || 'Anyone with the link'}</span>
                <span class="row-meta"><span>{ROLES.find(([r]) => r === i.role)?.[1]}</span>{i.team && <span>joins {i.team}</span>}
                  {i.first_prompt && <span>with a first session</span>}<span>expires {when(i.expires_at)}</span></span>
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
        <Group title="Default team" hint="Where your Slack direct messages go, and the team new sessions, projects and automations start in. Unless you choose, it's the team you were invited to, else your personal team.">
          <select aria-label="Default team" value={me.default_team_id} onChange={e => save({ default_team_id: e.currentTarget.value })}>
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
