import type { ComponentChildren } from 'preact';
import { useEffect, useLayoutEffect, useReducer, useRef, useState } from 'preact/hooks';
import {
  api, get, onGlobal, subscribe, withFiles, type Environment, type Ev, type Info, type Prompt, type PullRequest, type Session,
} from './api';
import { AttachButton, Attachments, PendingFiles, useFiles } from './attach';
import { useCapabilities } from './capabilities';
import { span } from './insights';
import { Link, Markdown, safeHref } from './markdown';
import { TeamLabel, VisibilityBadge, useTeamIndex } from './teams';
import { Icon, useDismiss } from './components';
import { ShownCard, Viewer, ViewerContext, type Shown } from './viewer';
import { Chip, Dot, MODELS, Spinner, ago, dur, effortLevels, fail, modEnter, money, useLoad, useTick } from './util';

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
  caps: { enabled: string[]; suggested: string[] }; // the sandbox's capabilities, and those its repositories suggest
  files: Shown[]; // shown in the viewer, oldest first
  seq: number;
};
const newModel = (): Model => ({
  prompts: {}, blocks: [], groups: {}, results: {}, ends: {}, deltas: {}, logs: {}, caps: { enabled: [], suggested: [] }, files: [], seq: 0,
});

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
  if (e.type === 'artifact' && d.kind === 'file' && !m.files.some(f => f.id === d.id)) m.files.push(d); // also on the timeline
  if (e.type === 'capability') { // added during the session; also shown on the timeline
    m.caps = { enabled: [...new Set([...m.caps.enabled, d.id])], suggested: m.caps.suggested.filter(x => x !== d.id) };
  }
  switch (e.type) {
    case 'capabilities': m.caps = { enabled: d.enabled || [], suggested: d.suggested || [] }; return;
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
  const [viewing, setViewing] = useState(''); // the shown file open in the viewer
  const [viewer, setViewer] = useState(false);
  const openFile = (fid: string) => { setViewing(fid); setViewer(true); };
  useTick();

  useEffect(() => {
    let raf = 0;
    const bump = () => { raf ||= requestAnimationFrame(() => { raf = 0; rerender(0); }); };
    const loadKids = () => get<Session[]>(`/api/sessions?parent=${id}`).then(setKids, () => {});
    const stop = subscribe(() => `/api/sessions/${id}/events${m.seq ? '?after=' + m.seq : ''}`, e => {
      apply(m, e);
      if (e.type === 'child') loadKids();
      // A file the agent shows just now opens; replayed ones wait for a click.
      if (e.type === 'artifact' && e.data?.kind === 'file' && Date.now() - Date.parse(e.at) < 60_000) openFile(e.data.id);
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
  const files = m.files.length > 0 && (
    <button type="button" class={'btn' + (viewer ? ' on' : '')} aria-pressed={viewer} onClick={() => setViewer(!viewer)}>
      <Icon name="panel" size={14} /> Files <span class="dim">{new Set(m.files.map(f => f.path)).size}</span>
    </button>
  );
  return (
    <ViewerContext.Provider value={openFile}>
      <div class="split">
        <div class="session">
          <Header s={s} kids={kids} stats={<Stats m={m} prs={prs} />} caps={m.caps} changed={x => { m.session = x; rerender(0); }} extra={files} />
          <div class="timeline" ref={scroller} onScroll={() => {
            const el = scroller.current!;
            stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          }}>
            <div class="col">
              {m.blocks.map((b, i) => 'items' in b
                // Queued prompts wait in the composer's queue, and the strip above it shows a stopped sandbox.
                ? m.prompts[b.id]?.status !== 'queued' && <PromptGroup key={b.id} g={b} m={m} />
                : !(i === m.blocks.length - 1 && b.type === 'sandbox' && b.data?.state === s.sandbox && s.status !== 'running')
                  && <Item key={b.seq} e={b} m={m} live={false} />)}
            </div>
          </div>
          <Composer m={m} s={s} prs={prs} />
        </div>
        {viewer && m.files.length > 0 && <Viewer session={s.id} files={m.files} open={viewing} setOpen={setViewing} close={() => setViewer(false)} />}
      </div>
    </ViewerContext.Provider>
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

function Header({ s, kids, stats, caps, changed, extra }: {
  s: Session; kids: Session[]; stats: ComponentChildren; caps: Model['caps']; changed: (s: Session) => void; extra?: ComponentChildren;
}) {
  const team = useTeamIndex()(s.team_id);
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
        {(s.team_id || s.environment_id) && (
          <div class="crumbs">
            {s.team_id && <TeamLabel team={team} />}
            {s.team_id && s.environment_id && <span class="dim">›</span>}
            {s.environment_id && env !== undefined && (env
              ? <a href={`#/settings/environments/${s.environment_id}`}>{env}</a>
              : <span class="dim">project deleted</span>)}
          </div>
        )}
        <div class="s-top">
          <h1 class="s-title">{s.title || 'Untitled'}</h1>
          <div class="actions">
            {extra}
            <button class="btn" disabled={busy} onClick={act(() => api('POST', `${base}/archive`, { archived: !s.archived }))}>
              {s.archived ? 'Unarchive' : 'Archive'}
            </button>
          </div>
        </div>
        <div class="meta">
          <Chip status={s.status} />
          <VisibilityBadge s={s} onChange={changed} />
          {s.author.name && <span>by {s.author.name}</span>}
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
        <SessionCapabilities caps={caps} />
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

/** The toolchains the session's sandbox has: those its repositories need,
 * found by the sandbox, and any the agent added. Nobody picks them here. */
function SessionCapabilities({ caps }: { caps: Model['caps'] }) {
  const list = useCapabilities();
  if (!list || !caps.enabled.length) return null;
  const name = (c: string) => list.find(x => x.id === c)?.name || c;
  return (
    <div class="meta caps-row" title="Toolchains installed in the sandbox: found from the repositories' files or added by the agent">
      {caps.enabled.map(c => <span key={c} class="badge plain">{name(c)}</span>)}
    </div>
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
    case 'capability':
      return (
        <div class="line dim">
          Added the {d.name} capability{d.by === 'agent' ? ' (by the agent)' : ''}
          {d.kept === 'environment' ? `, kept in the ${d.where} project` : d.kept === 'repository' ? `, recorded in ${d.where}` : ', for this session'}
        </div>
      );
    case 'artifact':
      if (d.kind === 'file') return <ShownCard f={d} />;
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

/** The follow-up box: a strip with what the session is doing, the prompts
 * queued behind it, then one card with the text, files and the model and
 * effort for this prompt. */
function Composer({ m, s, prs }: { m: Model; s: Session; prs: PullRequest[] }) {
  const [text, setText] = useState('');
  const [model, setModel] = useState('');
  const [effort, setEffort] = useState('');
  const [busy, setBusy] = useState(false);
  const [info] = useLoad<Info>('/api/info');
  const box = useRef<HTMLTextAreaElement>(null);
  const f = useFiles();
  const running = s.status === 'running';
  const empty = !text.trim() && !f.files.length;
  useLayoutEffect(() => { // grow with the text, up to the max-height in CSS
    const el = box.current;
    if (el) { el.style.height = 'auto'; el.style.height = el.scrollHeight + 'px'; }
  }, [text]);
  const send = async () => {
    if (empty || busy) return;
    setBusy(true);
    try {
      await api('POST', `/api/sessions/${s.id}/prompts`, withFiles({ content: text.trim(), model, effort }, f.files));
      setText('');
      f.clear();
    } catch (e) { fail(e); }
    setBusy(false);
  };
  const queued = Object.values(m.prompts).filter(p => p.status === 'queued').sort((a, b) => a.created_at.localeCompare(b.created_at));
  // Editing a queued prompt takes it back: there is no API to change one.
  const edit = async (p: Prompt) => {
    try {
      await cancel(p);
      setText(t => t.trim() ? t + '\n\n' + p.content : p.content);
      if (p.model) setModel(p.model);
      if (p.effort) setEffort(p.effort);
      box.current?.focus();
    } catch (e) { fail(e); }
  };
  const models = [...MODELS, ...(model && !MODELS.includes(model) ? [model] : [])];
  return (
    <form class={'composer' + (f.over ? ' dropping' : '')} onSubmit={e => { e.preventDefault(); send(); }}>
      <div class="col">
        <Status m={m} s={s} prs={prs} />
        {queued.length > 0 && (
          <div class="queue">
            {queued.map((p, i) => (
              <div key={p.id} class="queue-row">
                <span class="queue-n">{queued.length > 1 ? i + 1 : <Icon name="queue" size={13} />}</span>
                <span class="queue-text" title={p.content}>
                  {p.content || p.attachments?.map(a => a.name).join(', ')}
                  {!!p.attachments?.length && p.content && <span class="dim"> · {p.attachments.length} file{p.attachments.length > 1 ? 's' : ''}</span>}
                </span>
                {(p.model || p.effort) && <span class="dim queue-opts">{[p.model, p.effort].filter(Boolean).join(' · ')}</span>}
                {!p.attachments?.length && (
                  <button type="button" class="queue-act" title="Edit: take it back into the box" aria-label="Edit queued prompt"
                    onClick={() => edit(p)}><Icon name="pen" size={13} /></button>
                )}
                <button type="button" class="queue-act" title="Cancel this prompt" aria-label="Cancel queued prompt"
                  onClick={() => cancel(p).catch(fail)}><Icon name="x" size={13} /></button>
              </div>
            ))}
          </div>
        )}
        <div class="cbox">
          <PendingFiles files={f.files} remove={f.remove} />
          <textarea ref={box} rows={1} value={text} aria-label="Follow-up"
            placeholder={running ? (queued.length ? 'Queue another follow-up…' : 'Queue a follow-up…') : 'Send a follow-up…'}
            onInput={e => setText(e.currentTarget.value)} onKeyDown={modEnter(send)} onPaste={f.onPaste} />
          <div class="cbox-bar">
            <AttachButton add={f.add} />
            <Pick label="Model" title="Model for this prompt" value={model} set={setModel}
              def={s.model || info?.model} options={models} custom />
            <Pick label="Effort" title="Reasoning effort for this prompt" icon="gauge" value={effort} set={setEffort}
              def={s.effort || info?.effort} options={effortLevels()} />
            <span class="grow" />
            {!empty && <span class="cbox-hint"><kbd>⌘↵</kbd> to {running ? 'queue' : 'send'}</span>}
            <button class="send" disabled={busy || empty} title={(running ? 'Queue' : 'Send') + ' (⌘/Ctrl+Enter)'}
              aria-label={running ? 'Queue' : 'Send'}><Icon name="up" /></button>
          </div>
          {f.over && <div class="cbox-drop">Drop to attach <span class="dim">· up to 10 files, 50 MB</span></div>}
        </div>
      </div>
    </form>
  );
}

const cancel = (p: Prompt) => api('DELETE', `/api/sessions/${p.session_id}/prompts/${p.id}`);

/** A toolbar menu for an option of the follow-ups sent from here; "" is the
 * session's default. */
function Pick({ label, title, icon, value, set, def, options, custom }: {
  label: string; title: string; icon?: string; value: string; set: (v: string) => void; def?: string; options: string[]; custom?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(open, () => setOpen(false), ref);
  const pick = (v: string) => { set(v); setOpen(false); };
  return (
    <div class="pick" ref={ref}>
      <button type="button" class={'tb' + (value ? ' set' : '') + (open ? ' on' : '')} title={title} aria-haspopup="menu"
        aria-expanded={open} onClick={() => setOpen(!open)}>
        {icon && <Icon name={icon} size={14} />}
        <span>{value ? cap(value) : def ? cap(def) : label}</span>
        <Icon name="down" size={12} />
      </button>
      {open && (
        <div class="menu up" role="menu">
          <div class="menu-label">{label} for follow-ups</div>
          <button type="button" role="menuitemradio" aria-checked={!value} class={'menu-item' + (value ? '' : ' on')} onClick={() => pick('')}>
            Default {def && <span class="dim menu-note">{def}</span>}
          </button>
          <div class="menu-sep" />
          {options.map(o => (
            <button key={o} type="button" role="menuitemradio" aria-checked={o === value} class={'menu-item' + (o === value ? ' on' : '')}
              onClick={() => pick(o)}>{cap(o)}</button>
          ))}
          {custom && (
            <>
              <div class="menu-sep" />
              <input class="mono menu-input" placeholder="Model ID, then Enter" spellcheck={false} aria-label="Custom model ID"
                onKeyDown={e => {
                  const v = e.currentTarget.value.trim();
                  if (e.key === 'Enter') { e.preventDefault(); if (v) pick(v); }
                }} />
            </>
          )}
        </div>
      )}
    </div>
  );
}

const cap = (s: string) => (MODELS.includes(s) ? s[0].toUpperCase() + s.slice(1) : s);

/** What the session is doing, docked above the box: the running step and
 * Stop, a stopped sandbox, or how the last prompt ended. */
function Status({ m, s, prs }: { m: Model; s: Session; prs: PullRequest[] }) {
  const [busy, setBusy] = useState(false);
  useTick(s.status === 'running' ? 1000 : 30000);
  const run = Object.values(m.prompts).find(p => p.status === 'running');
  if (s.status === 'running' && run) {
    const g = m.groups[run.id];
    const tool = g && [...g.items].reverse().find(e => e.type === 'tool' && !e.data.parent);
    const step = s.sandbox === 'starting' ? <b>Starting sandbox</b>
      : tool && !m.results[run.id + ':' + tool.data.id]
        ? <><b>{tool.data.name}</b> <span class="mono ellipsis">{summarize(tool.data.input)}</span></>
        : <b>{m.deltas[run.id] ? 'Writing' : 'Working'}</b>;
    const stop = async () => {
      setBusy(true);
      try { await api('POST', `/api/sessions/${s.id}/stop`); } catch (e) { fail(e); }
      setBusy(false);
    };
    return (
      <div class="strip">
        <span class="strip-main"><Spinner />{step}</span>
        {run.started_at && <span class="strip-num">{dur(Date.now() - Date.parse(run.started_at))}</span>}
        <button type="button" class="btn sm" disabled={busy} onClick={stop}
          title="Stop this turn; queued prompts are cancelled too"><span class="stop-sq" />Stop</button>
      </div>
    );
  }
  if (s.sandbox === 'stopped' || s.sandbox === 'failed') {
    const last = [...m.blocks].reverse().find((b): b is Ev => !('items' in b) && b.type === 'sandbox')
      ?? [...m.blocks].reverse().flatMap(b => ('items' in b ? [...b.items].reverse() : [])).find(e => e.type === 'sandbox');
    const why = last?.data?.state === s.sandbox ? last.data.message : '';
    return (
      <div class={'strip ' + (s.sandbox === 'failed' ? 'bad' : 'warn')}>
        <span class="strip-main">
          <Dot status={s.sandbox === 'failed' ? 'failed' : 'stopped'} />
          <b>Sandbox {s.sandbox}</b>{why && <span class="ellipsis">· {why}.</span>}
          <span class="strip-then">Sending a follow-up {s.sandbox === 'failed' ? 'tries again' : 'wakes it'}.</span>
        </span>
      </div>
    );
  }
  const end = [...m.blocks].reverse().map(b => ('items' in b ? m.ends[b.id] : undefined)).find(Boolean);
  if (!end) return null;
  const links = s.repos.filter(r => r.pr_url).map(r => {
    const pr = prs.find(p => p.url === r.pr_url);
    return { url: r.pr_url!, label: pr ? `#${pr.number} ${pr.state}` : 'Pull request', title: r.repo };
  });
  return (
    <div class={'strip ' + (end.status === 'done' ? 'ok' : end.status === 'failed' ? 'bad' : '')}>
      <span class="strip-main">
        {end.status === 'done' ? <><span class="strip-ok">✓</span><b>Done</b></>
          : end.status === 'failed' ? <><b>✗ Failed</b>{end.error && <span class="ellipsis">{end.error}</span>}</>
          : <b>{end.duration_ms != null ? 'Stopped' : 'Cancelled'}</b>}
        <span class="strip-num">
          {[end.duration_ms != null && dur(end.duration_ms), money(end.cost_usd)].filter(Boolean).join(' · ')}
        </span>
      </span>
      {links.map(l => (
        <a key={l.url} href={safeHref(l.url)} target="_blank" rel="noopener noreferrer" title={l.title}>
          <Icon name="pr" size={13} /> {l.label}
        </a>
      ))}
    </div>
  );
}
