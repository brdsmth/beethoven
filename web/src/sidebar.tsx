import { Fragment, type ComponentChildren, type RefObject } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { api, get, onGlobal, type Account, type Environment, type Session } from './api';
import { TeamMark, TeamSwitcher, scopeTeam, useScope, useTeams } from './teams';
import { Dot, ago, fail, go, readPref, useTick, writePref } from './util';

// The sidebar: who it's for and how to search, the pages, then the
// sessions grouped by what they work on.

const ICONS: Record<string, string> = {
  plus: 'M8 3v10M3 8h10',
  search: 'M7 2.5a4.5 4.5 0 1 1 0 9 4.5 4.5 0 0 1 0-9zM10.5 10.5l3 3',
  bolt: 'M9 1.5 3.5 9H8l-1 5.5L12.5 7H8z',
  chart: 'M2.5 13.5h11M4.5 11V8M8 11V4.5M11.5 11V6.5',
  gear: 'M8 6a2 2 0 1 1 0 4 2 2 0 0 1 0-4zM8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4',
  repo: 'M4 2.5h8.5v10H4a1.5 1.5 0 0 0 0 3h8.5M4 2.5A1.5 1.5 0 0 0 2.5 4v10',
  project: 'M2.5 5 8 2l5.5 3v6L8 14l-5.5-3zM2.5 5 8 8l5.5-3M8 8v6',
  scratch: 'M3 13l1-3.5L10.5 3 13 5.5 6.5 12zM9 4.5 11.5 7',
  chat: 'M2.5 4.5a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2H7l-3 2.5v-2.5a2 2 0 0 1-1.5-2z',
  person: 'M8 2.5a2.5 2.5 0 1 1 0 5 2.5 2.5 0 0 1 0-5zM3 13.5c.6-2.3 2.6-3.5 5-3.5s4.4 1.2 5 3.5',
  caret: 'm6 4.5 3.5 3.5L6 11.5',
};

export const Icon = ({ name, size = 16 }: { name: string; size?: number }) => (
  <svg class="ico" width={size} height={size} viewBox="0 0 16 16" aria-hidden="true"><path d={ICONS[name]} /></svg>
);

type Tab = 'active' | 'running' | 'archived';
const COLLAPSED_PREF = 'beethoven.sidebar.collapsed';

/** True when a key press is aimed at a text field, not the page. */
const typing = (e: KeyboardEvent) => {
  const t = e.target as HTMLElement | null;
  return !!t && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName));
};

/** Closes a popup on a press outside ref or on Escape. */
function useDismiss(open: boolean, close: () => void, ref: RefObject<HTMLElement>) {
  useEffect(() => {
    if (!open) return;
    const out = (e: Event) => { if (!ref.current?.contains(e.target as Node)) close(); };
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    addEventListener('pointerdown', out);
    addEventListener('keydown', esc);
    return () => { removeEventListener('pointerdown', out); removeEventListener('keydown', esc); };
  }, [open]);
}

/** The sessions in the switcher's scope, archived or not, kept live; none
 * while off. */
function useSessions(archived: boolean, on = true) {
  const [list, setList] = useState<Session[]>();
  const [scope] = useScope();
  const filter = scope === 'all' ? '' : '&team=' + scope;
  useEffect(() => {
    if (!on) { setList(undefined); return; }
    const team = scopeTeam(scope);
    const fits = (s: Session) => scope === 'all' || (scope === 'workspace' ? !s.team_id : s.team_id === team);
    const load = () => get<Session[]>(`/api/sessions?limit=200${archived ? '&archived=1' : ''}${filter}`).then(setList, fail);
    load();
    return onGlobal(e => {
      if (e.type === 'session_hidden') setList(l => (l || []).filter(x => x.id !== e.data.id));
      if (e.type === 'session') {
        const s: Session = e.data;
        setList(l => {
          const rest = (l || []).filter(x => x.id !== s.id);
          return s.archived === archived && fits(s) ? [s, ...rest] : rest;
        });
      }
    }, load); // a reconnect may have missed changes
  }, [archived, filter, on]);
  return list;
}

type Group = { key: string; label: string; kind: 'project' | 'repo' | 'scratch'; sessions: Session[] };

/** Sessions grouped by what they work on: a project, a repository, or a
 * scratch workspace; the busiest group first. */
function groups(list: Session[], envs?: Environment[]) {
  const out = new Map<string, Group>();
  for (const s of list) {
    const key = s.environment_id ? 'env:' + s.environment_id : s.repos[0]?.repo || 'scratch';
    const label = s.environment_id ? envs?.find(e => e.id === s.environment_id)?.name || "Another team's project"
      : s.repos[0]?.repo || 'Scratch';
    const kind = s.environment_id ? 'project' : s.repos.length ? 'repo' : 'scratch';
    if (!out.has(key)) out.set(key, { key, label, kind, sessions: [] });
    out.get(key)!.sessions.push(s);
  }
  return [...out.values()];
}

type Row = { s: Session; depth: number; orphan: boolean };

/** A group's sessions with each child under its parent; a child whose
 * parent isn't listed stands alone, marked as a child. */
function nest(list: Session[]): Row[] {
  const ids = new Set(list.map(s => s.id));
  const kids = new Map<string, Session[]>();
  for (const s of list) if (s.parent_id && ids.has(s.parent_id)) kids.set(s.parent_id, [...(kids.get(s.parent_id) || []), s]);
  const out: Row[] = [];
  const walk = (s: Session, depth: number) => {
    out.push({ s, depth, orphan: depth === 0 && !!s.parent_id });
    kids.get(s.id)?.forEach(k => walk(k, Math.min(depth + 1, 3)));
  };
  list.filter(s => !s.parent_id || !ids.has(s.parent_id)).forEach(s => walk(s, 0));
  return out;
}

/** The number of a session's first pull request, from its URL. */
const prNumber = (s: Session) => {
  const url = s.repos.find(r => r.pr_url)?.pr_url;
  return url ? url.match(/\/pull\/(\d+)/)?.[1] || '' : undefined;
};

const initials = (a: Account) =>
  (a.name || a.email).split(/[\s@._-]+/).filter(Boolean).slice(0, 2).map(w => w[0].toUpperCase()).join('');

export function Sidebar({ current, account }: { current: string; account: Account | null }) {
  const [tab, setTab] = useState<Tab>('active');
  const [palette, setPalette] = useState(false);
  const [envs, setEnvs] = useState<Environment[]>();
  const [collapsed, setCollapsed] = useState<string[]>(() => readPref<string[]>(COLLAPSED_PREF) || []);
  const [scope] = useScope();
  const teams = useTeams();
  const active = useSessions(false);
  const archived = useSessions(true, tab === 'archived');
  useTick();
  useEffect(() => { get<Environment[]>('/api/environments').then(setEnvs, () => {}); }, [current]);
  useEffect(() => {
    const keys = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === 'k' && (e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey) {
        e.preventDefault();
        setPalette(p => !p);
      } else if (e.key === 'c' && !e.metaKey && !e.ctrlKey && !e.altKey && !e.repeat && !typing(e) && !document.querySelector('.palette')) {
        e.preventDefault();
        go('/');
      }
    };
    addEventListener('keydown', keys);
    return () => removeEventListener('keydown', keys);
  }, []);

  const running = active?.filter(s => s.status === 'running');
  const list = tab === 'archived' ? archived : tab === 'running' ? running : active;
  const toggle = (key: string) => {
    const next = collapsed.includes(key) ? collapsed.filter(k => k !== key) : [...collapsed, key];
    setCollapsed(next);
    writePref(COLLAPSED_PREF, next);
  };
  const teamOf = (id?: string) => teams?.find(t => t.id === id);
  const page = (href: string, name: string, icon: string) => (
    <a href={href} class={'side-nav-item' + (current === name ? ' active' : '')}><Icon name={icon} />{name[0].toUpperCase() + name.slice(1)}</a>
  );
  const empty = tab === 'archived' ? 'No archived sessions.' : tab === 'running' ? 'Nothing is running.' : 'No sessions yet.';

  return (
    <nav class="sidebar">
      <div class="side-head">
        {teams?.length ? <TeamSwitcher /> : <a class="brand" href="#/">Beethoven</a>}
      </div>
      <div class="side-tools">
        <a class="btn primary block" href="#/"><Icon name="plus" />New session<kbd title="Press C">C</kbd></a>
        <button type="button" class="side-search" onClick={() => setPalette(true)}>
          <Icon name="search" /><span>Search</span><kbd>{/Mac|iP/.test(navigator.platform) ? '⌘K' : 'Ctrl K'}</kbd>
        </button>
      </div>
      <div class="side-nav">
        {page('#/automations', 'automations', 'bolt')}
        {page('#/insights', 'insights', 'chart')}
        {page('#/settings', 'settings', 'gear')}
      </div>
      <div class="side-tabs">
        {([['active', 'Active', active?.length], ['running', 'Running', running?.length], ['archived', 'Archived', undefined]] as const)
          .map(([v, label, n]) => (
            <button type="button" key={v} class={tab === v ? 'on' : ''} aria-pressed={tab === v} onClick={() => setTab(v)}>
              {label}{!!n && <span class={'count' + (v === 'running' ? ' live' : '')}>{n}</span>}
            </button>
          ))}
      </div>
      <div class="side-list">
        {list?.length === 0 && <p class="empty small">{empty}</p>}
        {list && groups(list, envs).map(g => {
          const shut = collapsed.includes(g.key);
          return (
            <div key={g.key} class="side-group">
              <button type="button" class={'side-group-label' + (shut ? ' shut' : '')} aria-expanded={!shut} title={g.label}
                onClick={() => toggle(g.key)}>
                <Icon name="caret" size={12} /><Icon name={g.kind} size={13} />
                <span class="ellipsis">{g.label}</span>
                <span class="count">{g.sessions.length}</span>
              </button>
              {!shut && nest(g.sessions).map(({ s, depth, orphan }) => {
                const pr = prNumber(s);
                return (
                  <a key={s.id} href={`#/s/${s.id}`} class={'item' + (s.id === current ? ' active' : '')}
                    style={depth ? { paddingLeft: 8 + depth * 16 } : undefined}>
                    <Dot status={s.status} />
                    <span class="item-main">
                      <span class="item-title">{orphan && <span class="dim">↳ </span>}{s.title || 'Untitled'}</span>
                      <span class="item-meta">
                        {s.status === 'running' && <span>Running</span>}
                        {s.status === 'failed' && <span class="err">Failed</span>}
                        {pr !== undefined && s.status !== 'running' && <span class="ok">{pr ? `PR #${pr}` : 'PR'}</span>}
                        {scope === 'all' && s.team_id && <TeamMark team={teamOf(s.team_id)} size={11} />}
                        {s.visibility === 'private' && <span title="Only you">🔒</span>}
                        {s.author.name && <span class="ellipsis">{s.author.name}</span>}
                        <span>{ago(s.updated_at)}</span>
                      </span>
                    </span>
                  </a>
                );
              })}
            </div>
          );
        })}
      </div>
      {account && <AccountMenu account={account} current={current} />}
      {palette && <Palette envs={envs} close={() => setPalette(false)} />}
    </nav>
  );
}

function AccountMenu({ account, current }: { account: Account; current: string }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(open, () => setOpen(false), ref);
  useEffect(() => setOpen(false), [current]);
  return (
    <div class="side-account" ref={ref}>
      <span class="avatar" aria-hidden="true">{initials(account)}</span>
      <span class="side-account-who">
        <span class="ellipsis">{account.name || account.email}</span>
        {account.name && <span class="ellipsis dim">{account.email}</span>}
      </span>
      <button type="button" class="btn icon" aria-label="Account menu" aria-haspopup="menu" aria-expanded={open}
        onClick={() => setOpen(!open)}>⋯</button>
      {open && (
        <div class="menu up" role="menu">
          <a class="menu-item" role="menuitem" href="#/settings/account">Your account</a>
          <button type="button" class="menu-item" role="menuitem"
            onClick={() => api('POST', '/api/auth/logout').then(() => location.reload(), fail)}>Sign out</button>
        </div>
      )}
    </div>
  );
}

type Hit = { key: string; group: string; label: string; hint?: string; path: string; icon: ComponentChildren };

const PAGES: [string, string, string][] = [
  ['New session', '/', 'plus'], ['Automations', '/automations', 'bolt'], ['Insights', '/insights', 'chart'],
  ['Settings', '/settings', 'gear'], ['New project', '/projects/new', 'project'], ['Your account', '/settings/account', 'person'],
];

/** ⌘K: jump to a session, a project or a page by typing part of its name. */
function Palette({ envs, close }: { envs?: Environment[]; close: () => void }) {
  const [q, setQ] = useState('');
  const [found, setFound] = useState<Session[]>();
  const [sel, setSel] = useState(0);
  const ref = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  useDismiss(true, close, ref);
  useEffect(() => input.current?.focus(), []); // autofocus works once per page
  const text = q.trim().toLowerCase();
  useEffect(() => {
    let live = true;
    const t = setTimeout(() => {
      get<Session[]>(`/api/sessions?archived=all&limit=12${text ? '&q=' + encodeURIComponent(text) : ''}`)
        .then(l => { if (live) setFound(l); }, () => {});
    }, text ? 120 : 0);
    return () => { live = false; clearTimeout(t); };
  }, [text]);
  useEffect(() => setSel(0), [text]);

  const match = (s: string) => s.toLowerCase().includes(text);
  const where = (s: Session) => (s.environment_id && envs?.find(e => e.id === s.environment_id)?.name) || s.repos[0]?.repo || 'scratch';
  const hits: Hit[] = [
    ...(found || []).map(s => ({
      key: s.id, group: 'Sessions', label: s.title || 'Untitled', path: '/s/' + s.id, icon: <Dot status={s.status} />,
      hint: s.archived ? `archived · ${ago(s.updated_at)}` : where(s),
    })),
    ...(text ? envs || [] : []).filter(e => match(e.name)).slice(0, 5).map(e => ({
      key: e.id, group: 'Projects', label: e.name, path: '/settings/environments/' + e.id, icon: <Icon name="project" />,
    })),
    ...PAGES.filter(([label]) => match(label)).map(([label, path, icon]) => ({
      key: path, group: 'Pages', label, path, icon: <Icon name={icon} />,
    })),
  ];
  const pick = (h?: Hit) => { if (!h) return; close(); go(h.path); };
  const keys = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const n = hits.length;
      if (n) setSel(i => (i + (e.key === 'ArrowDown' ? 1 : n - 1)) % n);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      pick(hits[sel]);
    }
  };
  useEffect(() => { ref.current?.querySelector('.palette-hit.on')?.scrollIntoView({ block: 'nearest' }); }, [sel]);

  return (
    <div class="palette-scrim">
      <div class="palette" ref={ref} role="dialog" aria-label="Search">
        <input ref={input} placeholder="Search sessions, projects and pages…" value={q} aria-activedescendant={hits[sel]?.key}
          onInput={e => setQ(e.currentTarget.value)} onKeyDown={keys} />
        <div class="palette-hits" role="listbox">
          {found && !hits.length && <p class="empty small">Nothing matches.</p>}
          {hits.map((h, i) => (
            <Fragment key={h.key}>
              {(i === 0 || hits[i - 1].group !== h.group) && <div class="palette-group">{h.group}</div>}
              <a id={h.key} role="option" aria-selected={i === sel} href={'#' + h.path}
                class={'palette-hit' + (i === sel ? ' on' : '')} onMouseMove={() => setSel(i)} onClick={e => { e.preventDefault(); pick(h); }}>
                {h.icon}<span class="ellipsis">{h.label}</span>{h.hint && <span class="palette-hint ellipsis">{h.hint}</span>}
              </a>
            </Fragment>
          ))}
        </div>
      </div>
    </div>
  );
}
