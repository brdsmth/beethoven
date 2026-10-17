import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import {
  api, reloadTeams, type Automation, type Environment, type Person, type Session, type SlackStatus, type Team, type TeamMember,
} from './api';
import { SecretsEditor } from './secrets';
import { setScope, useMe, useScope, useTeams, type Scope } from './state';
import { ConfirmButton, Dot, Group, Head, SaveBar, ago, errText, fail, go, plural, toast, useLoad } from './util';

// Teams group people and own work: sessions, projects, automations,
// secrets and Slack channels. The sidebar's switcher picks which team's
// work to show; new sessions start in it.

export { scopeTeam, setScope, useMe, useScope, useTeams, type Scope } from './state';

// Accents for teams without a color of their own, picked by id.
const PALETTE = ['#4f6bed', '#0e9f6e', '#d97706', '#db2777', '#7c3aed', '#0891b2', '#dc2626', '#65a30d'];
export function teamColor(t: Pick<Team, 'id' | 'color'>) {
  if (t.color) return t.color;
  let h = 0;
  for (const c of t.id) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

/** A team's colored initial. */
export function TeamMark({ team, size = 18 }: { team?: Pick<Team, 'id' | 'name' | 'color'>; size?: number }) {
  if (!team) return <span class="mark workspace" style={{ width: size, height: size }} aria-hidden="true">◇</span>;
  return (
    <span class="mark" style={{ background: teamColor(team), width: size, height: size, fontSize: size * 0.6 }} aria-hidden="true">
      {(team.name.trim()[0] || '?').toUpperCase()}
    </span>
  );
}

/** Where a team's pages live: in the settings, beside projects. */
export const teamHref = (slug?: string, tab?: string) => '#/settings/teams' + (slug ? '/' + slug + (tab ? '/' + tab : '') : '');

/** A team's mark and name, for meta lines. */
export const TeamLabel = ({ team }: { team?: Team }) =>
  team ? <a class="team-label" href={teamHref(team.slug)}><TeamMark team={team} size={14} /> {team.name}</a>
    : <span class="team-label"><TeamMark size={14} /> Workspace</span>;

/** The sidebar's team switcher: which team's work to show and start. */
export function TeamSwitcher() {
  const [s, set] = useScope();
  const list = useTeams();
  const me = useMe();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => { if (!ref.current?.contains(e.target as Node)) setOpen(false); };
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
    addEventListener('pointerdown', close);
    addEventListener('keydown', esc);
    return () => { removeEventListener('pointerdown', close); removeEventListener('keydown', esc); };
  }, [open]);
  const current = list?.find(t => t.id === s);
  useEffect(() => { if (s !== 'all' && s !== 'workspace' && list && !current) set('all'); }, [s, list]); // a team gone
  if (!list) return null;
  const mine = list.filter(t => t.role && !t.archived_at);
  const others = list.filter(t => !t.role && !t.archived_at);
  const pick = (v: Scope) => { set(v); setOpen(false); };
  const label = current ? current.name : s === 'workspace' ? 'Workspace' : 'All sessions';
  return (
    <div class="switcher" ref={ref}>
      <button type="button" class="switcher-btn" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>
        {current ? <TeamMark team={current} /> : <span class="mark all" aria-hidden="true">∗</span>}
        <span class="ellipsis">{label}</span>
        <span class="dim">▾</span>
      </button>
      {open && (
        <div class="menu" role="menu">
          <Item on={s === 'all'} pick={() => pick('all')}><span class="mark all">∗</span> All sessions</Item>
          <Item on={s === 'workspace'} pick={() => pick('workspace')}><TeamMark /> Workspace <span class="dim">· no team</span></Item>
          {mine.length > 0 && <div class="menu-label">Your teams</div>}
          {mine.map(t => <Item key={t.id} on={s === t.id} pick={() => pick(t.id)}><TeamMark team={t} /> {t.name}</Item>)}
          {others.length > 0 && <div class="menu-label">Other teams</div>}
          {others.map(t => <Item key={t.id} on={s === t.id} pick={() => pick(t.id)}><TeamMark team={t} /> {t.name}</Item>)}
          <div class="menu-sep" />
          <a class="menu-item" role="menuitem" href={teamHref()} onClick={() => setOpen(false)}>Browse teams</a>
          {current && <a class="menu-item" role="menuitem" href={teamHref(current.slug)} onClick={() => setOpen(false)}>About {current.name}</a>}
          <a class="menu-item" role="menuitem" href="#/projects/new" onClick={() => setOpen(false)}>New project…</a>
          {me?.admin && <a class="menu-item" role="menuitem" href={teamHref('new')} onClick={() => setOpen(false)}>New team</a>}
        </div>
      )}
    </div>
  );
}

function Item({ on, pick, children }: { on: boolean; pick: () => void; children: ComponentChildren }) {
  return <button type="button" role="menuitemradio" aria-checked={on} class={'menu-item' + (on ? ' on' : '')} onClick={pick}>{children}</button>;
}

// ---------------------------------------------------------------- the list

export function TeamsIndex() {
  const list = useTeams();
  const me = useMe();
  const join = (t: Team) => api('POST', `/api/teams/${t.id}/join`).then(() => { reloadTeams(); toast(`Joined ${t.name}`); }, fail);
  return (
    <>
      <Head title="Teams" actions={me?.admin && <a class="btn primary" href={teamHref('new')}>New team</a>}>
        A team groups people and owns their work: sessions, projects, automations, secrets and Slack channels. Its sessions
        are seen by its members unless made visible to everyone.
      </Head>
      {list?.length === 0 && <p class="empty">No teams yet.{me?.admin ? ' Create one to get started.' : ' An admin can create one.'}</p>}
      <div class="rows">
        {list?.map(t => (
          <div key={t.id} class="row">
            <TeamMark team={t} size={28} />
            <a class="row-main" href={teamHref(t.slug)}>
              <span class="row-title">{t.name} {t.archived_at && <span class="chip">archived</span>}</span>
              {t.description && <span class="row-desc">{t.description}</span>}
              <span class="row-meta">
                <span>{plural(t.members, 'member')}</span>
                <span>{t.join_policy === 'open' ? 'Anyone can join' : 'Invite only'}</span>
                {t.role && <span>You're {t.role === 'lead' ? 'a lead' : 'a member'}</span>}
              </span>
            </a>
            {!t.role && !t.archived_at && t.join_policy === 'open' && me?.account && <button class="btn" onClick={() => join(t)}>Join</button>}
          </div>
        ))}
      </div>
    </>
  );
}

// ------------------------------------------------------------- a new team

type TF = { name: string; slug: string; description: string; color: string; join_policy: 'open' | 'invite'; default_visibility: 'team' | 'workspace'; default_environment_id: string };
const toTF = (t?: Team): TF => ({
  name: t?.name || '', slug: t?.slug || '', description: t?.description || '', color: t?.color || '',
  join_policy: t?.join_policy || 'invite', default_visibility: t?.default_visibility || 'team', default_environment_id: t?.default_environment_id || '',
});

export function NewTeam() {
  const [f, setF] = useState<TF>(toTF());
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    try {
      const t = await api<Team>('POST', '/api/teams', f);
      reloadTeams();
      setScope(t.id);
      toast(`Created ${t.name}. Add its people next.`);
      go(teamHref(t.slug, 'members').slice(1));
    } catch (e) { setErr(errText(e)); setBusy(false); }
  };
  return (
    <>
      <a class="back" href={teamHref()}>‹ Teams</a>
      <Head title="New team">You'll lead it; add its people next.</Head>
      <form class="form" onSubmit={save}>
        <TeamFields f={f} set={v => { setF({ ...f, ...v }); setErr(''); }} />
        <SaveBar dirty={!!f.name.trim()} busy={busy} err={err} label="Create team" />
      </form>
    </>
  );
}

function TeamFields({ f, set, projects }: { f: TF; set: (v: Partial<TF>) => void; projects?: Environment[] }) {
  return (
    <>
      <div class="grid2">
        <label>Name<input required maxLength={60} placeholder="e.g. Web platform" value={f.name} onInput={e => set({ name: e.currentTarget.value })} /></label>
        <label>Short name<input class="mono" maxLength={40} placeholder="from the name" spellcheck={false} value={f.slug}
          onInput={e => set({ slug: e.currentTarget.value.toLowerCase() })} />
          <span class="hint">For links: <code>{teamHref(f.slug || 'web-platform')}</code></span></label>
      </div>
      <label>Description<input maxLength={300} placeholder="Optional: what the team works on" value={f.description}
        onInput={e => set({ description: e.currentTarget.value })} /></label>
      <div class="grid2">
        <label>Who can join
          <select value={f.join_policy} onChange={e => set({ join_policy: e.currentTarget.value as TF['join_policy'] })}>
            <option value="invite">Leads add people</option>
            <option value="open">Anyone can join</option>
          </select>
        </label>
        <label>New sessions are seen by
          <select value={f.default_visibility} onChange={e => set({ default_visibility: e.currentTarget.value as TF['default_visibility'] })}>
            <option value="team">The team</option>
            <option value="workspace">Everyone</option>
          </select>
        </label>
      </div>
      <div class="grid2">
        <label>Color
          <span class="color-row">
            <input type="color" aria-label="Color" value={f.color || '#4f6bed'} onInput={e => set({ color: e.currentTarget.value })} />
            {f.color && <button type="button" class="btn sm" onClick={() => set({ color: '' })}>Automatic</button>}
          </span>
        </label>
        {projects && (
          <label>Default project
            <select value={f.default_environment_id} onChange={e => set({ default_environment_id: e.currentTarget.value })}>
              <option value="">None</option>
              {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            <span class="hint">New sessions in the team start there.</span>
          </label>
        )}
      </div>
    </>
  );
}

// ------------------------------------------------------------- a team page

const TABS: [string, string][] = [
  ['overview', 'Overview'], ['members', 'Members'], ['projects', 'Projects'], ['automations', 'Automations'],
  ['secrets', 'Secrets'], ['channels', 'Channels'], ['settings', 'Settings'],
];

export function TeamPage({ slug, tab = 'overview' }: { slug: string; tab?: string }) {
  const [data, reload] = useLoad<{ team: Team; members: TeamMember[] }>(`/api/teams/${slug}`);
  const me = useMe();
  if (!data) return <p class="empty">Loading…</p>;
  const { team: t, members } = data;
  const changed = () => { reload(); reloadTeams(); };
  const tabs = TABS.filter(([k]) => k === 'settings' ? t.manages : k === 'channels' ? t.manages || !!t.role : true);
  return (
    <>
      <a class="back" href={teamHref()}>‹ Teams</a>
      <div class="team-head">
        <TeamMark team={t} size={36} />
        <div class="team-title">
          <h1>{t.name} {t.archived_at && <span class="chip">archived</span>}</h1>
          {t.description && <p class="muted">{t.description}</p>}
        </div>
        <div class="actions">
          {!t.role && !t.archived_at && t.join_policy === 'open' && me?.account &&
            <button class="btn primary" onClick={() => api('POST', `/api/teams/${t.id}/join`).then(changed, fail)}>Join</button>}
          {t.role && me?.account && <ConfirmButton label="Leave" ask="Leave team"
            onConfirm={() => api('DELETE', `/api/teams/${t.id}/members/${me.account!.id}`).then(changed)} />}
          <button class="btn" onClick={() => { setScope(t.id); go('/'); }}>Show its sessions</button>
        </div>
      </div>
      <nav class="tabs">
        {tabs.map(([k, label]) => <a key={k} class={'tab' + (k === tab ? ' on' : '')} href={teamHref(t.slug, k)}>{label}</a>)}
      </nav>
      <div class="tab-body">
        {tab === 'overview' && <Overview t={t} members={members} />}
        {tab === 'members' && <Members t={t} members={members} changed={changed} />}
        {tab === 'projects' && <Projects t={t} />}
        {tab === 'automations' && <TeamAutomations t={t} />}
        {tab === 'secrets' && <TeamSecrets t={t} />}
        {tab === 'channels' && <Channels t={t} />}
        {tab === 'settings' && t.manages && <TeamSettings t={t} changed={changed} />}
      </div>
    </>
  );
}

function Overview({ t, members }: { t: Team; members: TeamMember[] }) {
  const [sessions] = useLoad<Session[]>(`/api/sessions?team=${t.id}&limit=20`);
  const running = sessions?.filter(s => s.status === 'running') || [];
  return (
    <>
      <dl class="kv">
        <dt>People</dt><dd>{plural(members.length, 'member')}, led by {members.filter(m => m.team_role === 'lead').map(m => m.name || m.email || 'someone').join(', ') || 'nobody yet'}</dd>
        <dt>Joining</dt><dd>{t.join_policy === 'open' ? 'Anyone can join' : 'Leads add people'}</dd>
        <dt>New sessions</dt><dd>Seen by {t.default_visibility === 'team' ? 'the team' : 'everyone'}</dd>
      </dl>
      <Group title={running.length ? `Recent sessions · ${running.length} running` : 'Recent sessions'}>
        {sessions?.length === 0 && <p class="empty small">No sessions yet.</p>}
        <div class="rows">
          {sessions?.map(s => (
            <a key={s.id} class="row link" href={`#/s/${s.id}`}>
              <Dot status={s.status} />
              <span class="row-main">
                <span class="row-title">{s.title || 'Untitled'}</span>
                <span class="row-meta"><span>{s.author.name}</span><span>{ago(s.updated_at)}</span>{s.visibility !== 'team' && <span>{VIS[s.visibility]}</span>}</span>
              </span>
            </a>
          ))}
        </div>
      </Group>
    </>
  );
}

function Members({ t, members, changed }: { t: Team; members: TeamMember[]; changed: () => void }) {
  const [people] = useLoad<Person[]>(t.manages && '/api/people');
  const [add, setAdd] = useState('');
  const me = useMe();
  const set = (id: string, role: string) => api('PUT', `/api/teams/${t.id}/members/${id}`, { role }).then(changed, fail);
  const candidates = people?.filter(p => !p.disabled && !members.some(m => m.id === p.id)) || [];
  return (
    <>
      <div class="rows">
        {members.map(m => (
          <div key={m.id} class="row">
            <span class="row-main">
              <span class="row-title">{m.name || m.email}{m.id === me?.account?.id && <span class="dim"> (you)</span>}</span>
              {m.email && m.name && <span class="row-desc">{m.email}</span>}
            </span>
            {t.manages ? (
              <select aria-label="Role" value={m.team_role} onChange={e => set(m.id, e.currentTarget.value)}>
                <option value="lead">Lead</option>
                <option value="member">Member</option>
              </select>
            ) : <span class="badge plain">{m.team_role === 'lead' ? 'Lead' : 'Member'}</span>}
            {t.manages && m.id !== me?.account?.id && (
              <ConfirmButton small label="Remove" ask="Remove" onConfirm={() => api('DELETE', `/api/teams/${t.id}/members/${m.id}`).then(changed)} />
            )}
          </div>
        ))}
      </div>
      {t.manages && !t.archived_at && (
        <form class="inline-form" onSubmit={e => { e.preventDefault(); if (add) set(add, 'member').then(() => setAdd('')); }}>
          <select aria-label="Person" value={add} onChange={e => setAdd(e.currentTarget.value)}>
            <option value="">Add someone…</option>
            {candidates.map(p => <option key={p.id} value={p.id}>{p.name || p.email}</option>)}
          </select>
          <button class="btn" disabled={!add}>Add</button>
          {me?.admin && <a class="hint" href="#/settings/people">Invite someone new</a>}
        </form>
      )}
      <p class="hint">Leads manage the team: its people, secrets, channels and settings. Members use and manage its projects and automations.</p>
    </>
  );
}

function Projects({ t }: { t: Team }) {
  const [envs] = useLoad<Environment[]>('/api/environments');
  const own = envs?.filter(e => e.team_id === t.id);
  return (
    <>
      <div class="page-head small">
        <p class="muted">A project is a reusable workspace: repositories, scripts that prepare the sandbox, and secrets.</p>
        {t.role && <a class="btn" href={`#/settings/environments/new?team=${t.id}`}>New project</a>}
      </div>
      {own?.length === 0 && <p class="empty small">No projects yet.</p>}
      <div class="rows">
        {own?.map(e => (
          <a key={e.id} class="row link" href={`#/settings/environments/${e.id}`}>
            <span class="row-main">
              <span class="row-title">{e.name}{e.id === t.default_environment_id && <span class="chip">default</span>}</span>
              <span class="row-meta mono">{e.repos.map(r => r.repo).join(', ') || 'No repositories'}</span>
            </span>
            <span class="dim">›</span>
          </a>
        ))}
      </div>
    </>
  );
}

function TeamAutomations({ t }: { t: Team }) {
  const [list] = useLoad<Automation[]>('/api/automations');
  const own = list?.filter(a => a.team_id === t.id);
  return (
    <>
      <div class="page-head small">
        <p class="muted">Automations run a saved prompt on a schedule or a webhook, as the person who made them.</p>
        {t.role && <a class="btn" href={`#/automations/new?team=${t.id}`}>New automation</a>}
      </div>
      {own?.length === 0 && <p class="empty small">No automations yet.</p>}
      <div class="rows">
        {own?.map(a => (
          <a key={a.id} class="row link" href={`#/automations/${a.id}`}>
            <span class="row-main"><span class="row-title">{a.name}</span>
              <span class="row-meta"><span class="mono">{a.schedule || 'webhook / manual'}</span>{!a.enabled && <span>paused</span>}</span></span>
            <span class="dim">›</span>
          </a>
        ))}
      </div>
    </>
  );
}

function TeamSecrets({ t }: { t: Team }) {
  if (!t.manages) return <p class="muted">The team's secrets reach its sessions, after global secrets and before a project's. Leads manage them.</p>;
  return (
    <Group title="Team secrets" hint="Every session the team owns gets these, after global secrets and before its project's or repositories'.">
      <SecretsEditor scope={'team:' + t.id} />
    </Group>
  );
}

function Channels({ t }: { t: Team }) {
  const [st, reload] = useLoad<SlackStatus>('/api/slack');
  if (!st) return <p class="empty">Loading…</p>;
  const own = st.channels.filter(c => c.owner_team_id === t.id);
  const free = st.channels.filter(c => !c.owner_team_id);
  const bind = (c: { team_id: string; channel: string }, team: string) =>
    api('PUT', `/api/slack/channels/${c.team_id}/${c.channel}/team`, { owner_team_id: team }).then(reload, fail);
  return (
    <>
      <p class="muted">Requests in a team's Slack channels start the team's sessions, and only its members may make them.</p>
      {own.length === 0 && <p class="empty small">No channels yet.</p>}
      <div class="rows">
        {own.map(c => (
          <div key={c.channel} class="row">
            <span class="row-main"><span class="row-title">#{c.name || c.channel}</span></span>
            {t.manages && <button class="btn sm" onClick={() => bind(c, '')}>Release</button>}
          </div>
        ))}
      </div>
      {t.manages && free.length > 0 && (
        <form class="inline-form" onSubmit={e => {
          e.preventDefault();
          const v = (e.currentTarget.elements.namedItem('ch') as HTMLSelectElement).value;
          const c = free.find(x => x.channel === v);
          if (c) bind(c, t.id);
        }}>
          <select name="ch" aria-label="Channel">{free.map(c => <option key={c.channel} value={c.channel}>#{c.name || c.channel}</option>)}</select>
          <button class="btn">Give to {t.name}</button>
        </form>
      )}
      {t.manages && free.length === 0 && (
        <p class="hint">Channels appear here once someone has asked Beethoven in them and chosen what they work on.</p>
      )}
    </>
  );
}

function TeamSettings({ t, changed }: { t: Team; changed: () => void }) {
  const [f, setF] = useState<TF>(toTF(t));
  const [envs] = useLoad<Environment[]>('/api/environments');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const dirty = JSON.stringify(f) !== JSON.stringify(toTF(t));
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    try {
      const saved = await api<Team>('PUT', `/api/teams/${t.id}`, f);
      toast('Team saved');
      if (saved.slug !== t.slug) go(teamHref(saved.slug, 'settings').slice(1));
      changed();
    } catch (e) { setErr(errText(e)); }
    setBusy(false);
  };
  const archive = (archived: boolean) => api('POST', `/api/teams/${t.id}/archive`, { archived }).then(changed);
  return (
    <form class="form" onSubmit={save}>
      <TeamFields f={f} set={v => { setF({ ...f, ...v }); setErr(''); }} projects={envs?.filter(e => !e.team_id || e.team_id === t.id)} />
      <SaveBar dirty={dirty} busy={busy} err={err} />
      <Group title={t.archived_at ? 'Archived' : 'Archive'} hint={t.archived_at
        ? 'An archived team takes no new work. Its sessions, people and history stay.'
        : 'Archiving stops new work for the team: sessions, automations and Slack requests. Nothing is deleted.'}>
        <div>{t.archived_at
          ? <button type="button" class="btn" onClick={() => archive(false).catch(fail)}>Restore team</button>
          : <ConfirmButton label="Archive team" ask="Archive" onConfirm={() => archive(true)} />}</div>
      </Group>
    </form>
  );
}

export const VIS: Record<string, string> = { workspace: 'Everyone', team: 'Team', private: 'Only me' };

/** Who sees a session, and a way to change it for those who may. */
export function VisibilityBadge({ s, onChange }: { s: Session; onChange: (s: Session) => void }) {
  const me = useMe();
  const [busy, setBusy] = useState(false);
  const options: [string, string][] = [['workspace', 'Everyone'], ...(s.team_id ? [['team', 'Team'] as [string, string]] : []),
    ...(s.account_id && s.account_id === me?.account?.id ? [['private', 'Only me'] as [string, string]] : [])];
  if (!me?.people && !s.team_id) return null;
  const change = async (v: string) => {
    setBusy(true);
    try { onChange(await api<Session>('PUT', `/api/sessions/${s.id}/visibility`, { visibility: v })); } catch (e) { fail(e); }
    setBusy(false);
  };
  return (
    <label class="vis" title="Who can see this session">
      <span aria-hidden="true">{s.visibility === 'private' ? '🔒' : s.visibility === 'team' ? '👥' : '🌐'}</span>
      <select aria-label="Who can see this session" disabled={busy} value={s.visibility} onChange={e => change(e.currentTarget.value)}>
        {options.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        {!options.some(([v]) => v === s.visibility) && <option value={s.visibility}>{VIS[s.visibility]}</option>}
      </select>
    </label>
  );
}

/** Loads teams by id, for labels. */
export function useTeamIndex() {
  const list = useTeams();
  return (id?: string) => (id ? list?.find(t => t.id === id) : undefined);
}

