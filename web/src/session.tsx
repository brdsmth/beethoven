import type { ComponentChildren } from 'preact';
import { useEffect, useLayoutEffect, useReducer, useRef, useState } from 'preact/hooks';
import {
  api, get, onGlobal, subscribe, withFiles, type Environment, type Ev, type Prompt, type PullRequest, type Session,
} from './api';
import { AttachButton, Attachments, PendingFiles, useFiles } from './attach';
import { span } from './insights';
import { Link, Markdown, safeHref } from './markdown';
import { Chip, Dot, EffortSelect, Spinner, ago, dur, fail, modEnter, money, useTick } from './util';

type End = { id: string; status: string; error?: string; cost_usd: number; duration_ms?: number };
type Group = { id: string; items: Ev[] };

// The timeline model is mutated in place as events arrive and rendered at
// most once per animation frame.
type Model = {
  session?: Session;
  prompts: Record<string, Prompt>;
  blocks: (Group | Ev)[]; // prompt groups, plus events outside any prompt, in arrival order
  groups: Record<string, Group>;
  results: Record<string, { output: string; is_error?: boolean }>; // by prompt + ':' + tool call id
  ends: Record<string, End>;
  deltas: Record<string, string>; // live text per prompt, until its full message lands
  logs: Record<string, string[]>; // live sandbox boot output per prompt
  seq: number;
};
const newModel = (): Model => ({ prompts: {}, blocks: [], groups: {}, results: {}, ends: {}, deltas: {}, logs: {}, seq: 0 });

function group(m: Model, id: string) {
  if (!m.groups[id]) m.blocks.push((m.groups[id] = { id, items: [] }));
  return m.groups[id];
}

function apply(m: Model, e: Ev) {
  if (e.seq) {
    if (e.seq <= m.seq) return;
    m.seq = e.seq;
  }
  const p = e.prompt || '', d = e.data || {};
  const patch = (id: string, v: Partial<Prompt>) => { if (m.prompts[id]) m.prompts[id] = { ...m.prompts[id], ...v }; };
  switch (e.type) {
    case 'session': m.session = d; return;
    case 'delta': m.deltas[p] = (m.deltas[p] || '') + d.text; return;
    case 'log': (m.logs[p] ||= []).push(d.text); m.logs[p].splice(0, m.logs[p].length - 50); return;
    case 'tool.result': m.results[p + ':' + d.id] = d; return;
    case 'prompt': m.prompts[d.id] ||= d; group(m, d.id); return;
    case 'prompt.start': patch(d.id, { status: 'running', started_at: e.at }); return;
    case 'prompt.end':
      m.ends[d.id] = d;
      patch(d.id, { status: d.status, error: d.error, cost_usd: d.cost_usd });
      delete m.deltas[d.id];
      return;
    case 'text': if (!d.parent) delete m.deltas[p];
  }
  if (p) group(m, p).items.push(e);
  else m.blocks.push(e);
}

export function SessionView({ id }: { id: string }) {
  const [m] = useState(newModel);
  const [, rerender] = useReducer((n: number) => n + 1, 0);
  const [missing, setMissing] = useState('');
  const [kids, setKids] = useState<Session[]>([]);
  const [prs, setPrs] = useState<PullRequest[]>([]);
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  useTick();

  useEffect(() => {
    let raf = 0;
    const bump = () => { raf ||= requestAnimationFrame(() => { raf = 0; rerender(0); }); };
    const loadKids = () => get<Session[]>(`/api/sessions?parent=${id}`).then(setKids, () => {});
    const stop = subscribe(() => `/api/sessions/${id}/events${m.seq ? '?after=' + m.seq : ''}`, e => {
      apply(m, e);
      if (e.type === 'child') loadKids();
      bump();
    });
    get<{ session: Session; prompts: Prompt[]; pull_requests: PullRequest[] }>(`/api/sessions/${id}`).then(r => {
      m.session ||= r.session;
      for (const p of r.prompts) m.prompts[p.id] ||= p;
      setPrs(r.pull_requests);
      bump();
    }, e => { if (e.status === 404) stop(); setMissing(e.message); });
    loadKids();
    const off = onGlobal(e => {
      if (e.type === 'session' && e.data.parent_id === id) setKids(k => [e.data, ...k.filter(x => x.id !== e.data.id)]);
    });
    return () => { stop(); off(); cancelAnimationFrame(raf); };
  }, [id]);

  useEffect(() => { document.title = m.session ? `${m.session.title} · Beethoven` : 'Beethoven'; });
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  });

  const s = m.session;
  if (missing) return <div class="page"><p class="empty">{missing}</p></div>;
  if (!s) return <div class="page"><p class="empty">Loading…</p></div>;
  return (
    <div class="session">
      <Header s={s} kids={kids} stats={<Stats m={m} prs={prs} />} />
      <div class="timeline" ref={scroller} onScroll={() => {
        const el = scroller.current!;
        stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      }}>
        <div class="col">
          {m.blocks.map(b => 'items' in b ? <PromptGroup key={b.id} g={b} m={m} /> : <Item key={b.seq} e={b} m={m} live={false} />)}
        </div>
      </div>
      <Composer id={s.id} running={s.status === 'running'} />
    </div>
  );
}

/** How the session has gone: its cycles, the agents' working time and what
 * became of its pull requests. */
function Stats({ m, prs }: { m: Model; prs: PullRequest[] }) {
  const ran = Object.values(m.prompts).filter(p => p.started_at);
  const secs = ran.reduce((t, p) => {
    const ms = m.ends[p.id]?.duration_ms ?? (p.finished_at && p.started_at ? Date.parse(p.finished_at) - Date.parse(p.started_at) : 0);
    return t + ms / 1000;
  }, 0);
  const merged = prs.filter(p => p.state === 'merged'), open = prs.filter(p => p.state === 'open'), closed = prs.filter(p => p.state === 'closed');
  let pr = '';
  if (prs.length === 1) {
    const p = prs[0];
    pr = p.state === 'merged' && p.merged_at ? `PR merged in ${span((Date.parse(p.merged_at) - Date.parse(p.opened_at)) / 1000)}`
      : p.state === 'closed' ? 'PR closed unmerged' : 'PR open';
  } else if (prs.length > 1) {
    pr = `${prs.length} PRs: ` + [[merged, 'merged'], [open, 'open'], [closed, 'closed']].filter(([l]) => l.length)
      .map(([l, w]) => `${(l as PullRequest[]).length} ${w}`).join(', ');
  }
  if (!ran.length) return null;
  return (
    <>
      <span>{ran.length} {ran.length === 1 ? 'cycle' : 'cycles'}</span>
      {secs > 0 && <span>{span(secs)} agent time</span>}
      {pr && <span>{pr}</span>}
    </>
  );
}

function Header({ s, kids, stats }: { s: Session; kids: Session[]; stats: ComponentChildren }) {
  const [pr, setPr] = useState<{ url: string; warning: string }>();
  const [env, setEnv] = useState<string>(); // its name; "" once known to be deleted
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (s.environment_id) get<Environment>(`/api/environments/${s.environment_id}`).then(e => setEnv(e.name), () => setEnv(''));
  }, [s.environment_id]);
  const act = (fn: () => Promise<unknown>) => async () => {
    setBusy(true);
    try { await fn(); } catch (e) { fail(e); }
    setBusy(false);
  };
  const base = `/api/sessions/${s.id}`;
  return (
    <header class="s-head">
      <div class="col">
        <div class="s-top">
          <h1 class="s-title">{s.title || 'Untitled'}</h1>
          <div class="actions">
            {s.status === 'running' && <button class="btn" disabled={busy} onClick={act(() => api('POST', `${base}/stop`))}>Stop</button>}
            <button class="btn" disabled={busy} onClick={act(() => api('POST', `${base}/archive`, { archived: !s.archived }))}>
              {s.archived ? 'Unarchive' : 'Archive'}
            </button>
          </div>
        </div>
        <div class="meta">
          <Chip status={s.status} />
          {s.environment_id && env !== undefined && (env
            ? <a href={`#/settings/environments/${s.environment_id}`}>environment {env}</a>
            : <span class="dim">environment deleted</span>)}
          {!s.repos.length && <span>scratch workspace</span>}
          <span>sandbox {s.sandbox}</span>
          {s.effort && <span>effort {s.effort}</span>}
          <span>{money(s.cost_usd)}</span>
          {stats}
          {s.parent_id && <a href={`#/s/${s.parent_id}`}>parent session</a>}
          {s.automation_id && <a href={`#/automations/${s.automation_id}`}>automation</a>}
        </div>
        {s.repos.length > 0 && (
          <div class="s-repos">
            {s.repos.map((r, i) => (
              <div key={r.repo} class="s-repo">
                <span class="mono" title={i ? undefined : 'Primary repository'}>{r.repo}</span>
                <span class="mono dim" title={`From ${r.base_branch || 'the default branch'}`}>
                  {s.branch ? '⎇ ' + s.branch : r.base_branch && 'from ' + r.base_branch}
                </span>
                {r.pr_url ? <a class="btn sm" href={safeHref(r.pr_url)} target="_blank" rel="noopener noreferrer">View PR ↗</a>
                  : <button class="btn sm" disabled={busy} onClick={act(async () =>
                      setPr(await api('POST', `${base}/pr`, { repo: r.repo, title: '', body: '' })))}>Open PR</button>}
              </div>
            ))}
          </div>
        )}
        {kids.length > 0 && (
          <div class="meta kids">
            <span>Children</span>
            {kids.map(k => <a key={k.id} href={`#/s/${k.id}`}><Dot status={k.status} /> {k.title}</a>)}
          </div>
        )}
        {pr && (
          <div class="notice">
            Pull request: <Link href={pr.url}>{pr.url}</Link>
            {pr.warning && <div class="warn">{pr.warning}</div>}
          </div>
        )}
      </div>
    </header>
  );
}

function PromptGroup({ g, m }: { g: Group; m: Model }) {
  const p = m.prompts[g.id];
  const live = p?.status === 'running';
  // Subagent items nest under the tool call that spawned them.
  const calls = new Set(g.items.filter(e => e.type === 'tool').map(e => e.data.id));
  const top: Ev[] = [], sub: Record<string, Ev[]> = {};
  for (const e of g.items) {
    const parent = e.data?.parent;
    if (parent && calls.has(parent)) (sub[parent] ||= []).push(e);
    else top.push(e);
  }
  // A run of sandbox events shows only its latest state.
  const shown = top.filter((e, i) => !(e.type === 'sandbox' && top[i + 1]?.type === 'sandbox'));
  const delta = m.deltas[g.id];
  const logs = live && m.session?.sandbox === 'starting' ? m.logs[g.id] : undefined;
  const end = m.ends[g.id];
  const waiting = g.items.some(e => e.type === 'tool' && !m.results[g.id + ':' + e.data.id]);
  return (
    <section class="group">
      {p && <UserBubble p={p} />}
      {shown.map(e => <Item key={e.seq} e={e} m={m} live={live} sub={sub} />)}
      {logs && logs.length > 0 && (
        <details class="bootlog" open><summary>Boot log</summary><pre>{logs.join('\n')}</pre></details>
      )}
      {delta && <div class="msg streaming"><Markdown text={delta} /></div>}
      {live && !delta && !waiting && <div class="line dim"><Spinner /> Working…</div>}
      {end && <Footer end={end} />}
    </section>
  );
}

function UserBubble({ p }: { p: Prompt }) {
  return (
    <div class="user">
      <div class="user-head">
        <b>{p.author?.name || 'You'}</b>
        <span class="dim">{ago(p.created_at)}</span>
        {p.source !== 'web' && <span class="tag">{p.source}</span>}
        <Chip status={p.status} />
        {p.status === 'queued' && (
          <button class="x" title="Cancel this prompt" aria-label="Cancel prompt"
            onClick={() => api('DELETE', `/api/sessions/${p.session_id}/prompts/${p.id}`).catch(fail)}>✕</button>
        )}
      </div>
      {p.content && <div class="pre">{p.content}</div>}
      {!!p.attachments?.length && <Attachments session={p.session_id} files={p.attachments} />}
    </div>
  );
}

function Item({ e, m, live, sub }: { e: Ev; m: Model; live: boolean; sub?: Record<string, Ev[]> }) {
  const d = e.data || {};
  switch (e.type) {
    case 'text':
      return <div class={'msg' + (d.parent ? ' nested' : '')}><Markdown text={d.text} /></div>;
    case 'thinking':
      return <details class="thinking"><summary>Thinking</summary><div class="pre">{d.text}</div></details>;
    case 'tool':
      return <ToolRow call={d} result={m.results[e.prompt + ':' + d.id]} live={live}>
        {sub?.[d.id] && (
          <details class="sub">
            <summary>Subagent · {sub[d.id].length} step{sub[d.id].length > 1 ? 's' : ''}</summary>
            {sub[d.id].map(x => <Item key={x.seq} e={x} m={m} live={live} />)}
          </details>
        )}
      </ToolRow>;
    case 'sandbox': {
      const text = d.state === 'starting' ? 'Starting sandbox' + (d.phase ? ' · ' + d.phase : '')
        : d.state === 'ready' ? 'Sandbox ready'
        : d.state === 'failed' ? 'Sandbox failed: ' + d.message
        : `Sandbox ${d.state}${d.message ? ' · ' + d.message : ''}`;
      return <div class={'line ' + (d.state === 'failed' ? 'err' : 'dim')}>{text}</div>;
    }
    case 'warning':
      return <div class="line warn">Warning: {d.message}</div>;
    case 'artifact':
      return (
        <div class="card">
          Pull request opened{d.repo && <> in <span class="mono">{d.repo}</span></>}: <Link href={d.url}>{d.title || d.url}</Link>
        </div>
      );
    case 'child':
      return (
        <div class="line">
          Child session <a href={`#/s/${d.id}`}>{d.title || d.id}</a> <Chip status={d.status} />
          {d.error && <span class="err"> {d.error}</span>}
        </div>
      );
    default:
      return <div class="line dim">{e.type}</div>;
  }
}

const SUMMARY_KEYS = ['command', 'file_path', 'path', 'pattern', 'url', 'query', 'description', 'prompt'];
function summarize(input: any): string {
  if (input == null || typeof input !== 'object') return String(input ?? '');
  const k = SUMMARY_KEYS.find(k => typeof input[k] === 'string');
  return k ? input[k].split('\n')[0] : JSON.stringify(input);
}

function ToolRow({ call, result, live, children }: {
  call: { id: string; name: string; input: unknown };
  result?: { output: string; is_error?: boolean };
  live: boolean;
  children?: ComponentChildren;
}) {
  const [open, setOpen] = useState(false);
  const state = result ? (result.is_error ? 'err' : 'ok') : live ? 'run' : 'na';
  return (
    <div class={'tool ' + state}>
      <button class="tool-head" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span class="tool-icon">{state === 'run' ? <Spinner /> : state === 'err' ? '✗' : '›'}</span>
        <span class="tool-name">{call.name}</span>
        <span class="tool-sum">{summarize(call.input)}</span>
      </button>
      {open && (
        <div class="tool-body">
          <pre>{JSON.stringify(call.input, null, 2)}</pre>
          {result ? <pre class="tool-out">{result.output || '(no output)'}</pre>
            : <div class="dim">{live ? 'Running…' : 'No result.'}</div>}
        </div>
      )}
      {children}
    </div>
  );
}

function Footer({ end }: { end: End }) {
  if (end.status === 'done') {
    return <div class="foot ok">✓ Done · {money(end.cost_usd)}{end.duration_ms != null && ' · ' + dur(end.duration_ms)}</div>;
  }
  if (end.status === 'failed') return <div class="foot err">✗ Failed{end.error && ' — ' + end.error}</div>;
  return <div class="foot">{end.duration_ms != null ? 'Stopped' : 'Cancelled'}</div>;
}

function Composer({ id, running }: { id: string; running: boolean }) {
  const [text, setText] = useState('');
  const [effort, setEffort] = useState('');
  const [busy, setBusy] = useState(false);
  const f = useFiles();
  const empty = !text.trim() && !f.files.length;
  const send = async () => {
    if (empty || busy) return;
    setBusy(true);
    try {
      await api('POST', `/api/sessions/${id}/prompts`, withFiles({ content: text.trim(), effort }, f.files));
      setText('');
      f.clear();
    } catch (e) { fail(e); }
    setBusy(false);
  };
  return (
    <form class={'composer' + (f.over ? ' dropping' : '')} onSubmit={e => { e.preventDefault(); send(); }} {...f.drop}>
      <div class="col">
        <PendingFiles files={f.files} remove={f.remove} />
        <div class="composer-row">
          <textarea rows={2} value={text} placeholder={running ? 'Queue a follow-up…' : 'Send a follow-up…'}
            onInput={e => setText(e.currentTarget.value)} onKeyDown={modEnter(send)} onPaste={f.onPaste} />
          <span class="composer-side">
            <AttachButton add={f.add} />
            <span class="compact" title="Reasoning effort for this prompt"><EffortSelect value={effort} set={setEffort} def="Effort" /></span>
            <button class="btn primary" disabled={busy || empty} title="⌘/Ctrl+Enter">{running ? 'Queue' : 'Send'}</button>
          </span>
        </div>
      </div>
    </form>
  );
}
