import { useEffect, useState } from 'preact/hooks';
import type { InsightGroup, InsightPerson, InsightRun, Reliability } from './api';
import {
  ChartTable, Columns, Funnel, Heatmap, Lines, RankBars, Spark, StackBar, Tile, amountDelta, cash, compact, one, pct, percentDelta,
  pointsDelta, shortDate, span, type Series,
} from './charts';
import { SCOPE_NOTE, daily, type Filters, type TabProps } from './insights';
import { Seg, ago, plural } from './util';

// The tabs past the overview. Each reads the same report, except
// Reliability, which has its own for admins.

const ORIGINS: Series[] = [
  { label: 'Web', cls: 'c1' }, { label: 'CLI', cls: 'c2' }, { label: 'Slack', cls: 'c3' }, { label: 'Automations', cls: 'c4' }, { label: 'Agents', cls: 'c5' },
];
const ORIGIN_KEYS = ['web', 'cli', 'slack', 'automation', 'agent'];
const originName = (key: string) => ORIGINS[ORIGIN_KEYS.indexOf(key)]?.label ?? key;
const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** The origins a scope can have, as indexes into ORIGINS. */
const scopeOrigins = (f: Filters) =>
  f.scope === 'people' ? [0, 1, 2] : f.scope === 'automations' ? [3] : f.scope === 'agents' ? [4] : [0, 1, 2, 3, 4];

const Empty = ({ what = 'Nothing in this period.' }: { what?: string }) => <p class="empty small">{what}</p>;

// ------------------------------------------------------------------- usage

export function Usage({ r, f }: TabProps) {
  const [by, setBy] = useState<'repo' | 'project' | 'team'>('repo');
  const shown = scopeOrigins(f);
  const e = r.ended, finished = e.done + e.failed + e.stopped;
  const groups = by === 'repo' ? r.by_repo : by === 'project' ? r.by_project : r.by_team;
  return (
    <>
      <section class="card viz-card">
        <h2>Sessions per day</h2>
        <p class="hint">{cap(SCOPE_NOTE[f.scope])}, by where they started.</p>
        <Columns label="Sessions per day by origin" series={shown.map(i => ORIGINS[i])}
          rows={r.days.map(d => ({ label: shortDate(d.date), values: shown.map(i => d.origins[i]) }))} />
      </section>
      <div class="grid2 pair">
        <section class="card">
          <h2>Where sessions start</h2>
          <RankBars rows={r.sources.map(s => ({ label: originName(s.label), value: s.n, sub: pct(s.n / Math.max(1, r.totals.sessions)) }))} />
        </section>
        <section class="card">
          <h2>How sessions ended</h2>
          <p class="hint">{finished ? <><b class="ink">{pct(e.done / finished)}</b> of finished sessions ended done.</> : 'None has finished yet.'}</p>
          <StackBar parts={[
            { label: 'Done', n: e.done, cls: 's1' }, { label: 'Failed', n: e.failed, cls: 's3' },
            { label: 'Stopped', n: e.stopped, cls: 's2' }, { label: 'Idle or running', n: e.active, cls: 's0' },
          ]} />
          <dl class="kv">
            <dt>Archived</dt><dd>{e.archived}</dd>
            <dt>Prompts per session</dt><dd>{one(r.totals.sessions ? r.totals.prompts / r.totals.sessions : 0)}</dd>
            <dt>Spawned sessions per session</dt><dd>{one(r.children_per_root)}</dd>
          </dl>
        </section>
      </div>
      <section class="card viz-card">
        <h2>When work starts</h2>
        <p class="hint">Sessions created by day of the week and hour, in your timezone.</p>
        <Heatmap cells={r.hours} />
      </section>
      <section class="card">
        <div class="card-head">
          <h2>Breakdown</h2>
          <span class="push"><Seg value={by} set={setBy} options={[['repo', 'Repository'], ['project', 'Project'], ['team', 'Team']]} /></span>
        </div>
        <GroupTable name={by === 'repo' ? 'Repository' : by === 'project' ? 'Project' : 'Team'} rows={groups} />
      </section>
      <section class="card">
        <h2>Model and effort</h2>
        <p class="hint">What sessions ran on, and how it turned out.</p>
        {r.by_model_effort.length === 0 ? <Empty /> : (
          <div class="table-wrap"><table class="data-table">
            <thead><tr><th>Model</th><th>Effort</th><th class="num">Sessions</th><th class="num">Share</th><th class="num">First try</th>
              <th class="num">Cycles</th><th class="num">Per session</th></tr></thead>
            <tbody>{r.by_model_effort.map(g => {
              const [model, effort] = g.name.split(' · ');
              return (
                <tr key={g.name}><td>{model}</td><td><span class="chip">{effort}</span></td><td class="num">{g.sessions}</td>
                  <td class="num">{pct(g.sessions / Math.max(1, r.totals.sessions))}</td><td class="num">{pct(g.settled ? g.first_try_rate : null)}</td>
                  <td class="num">{one(g.cycles)}</td><td class="num">{cash(g.spend / Math.max(1, g.sessions))}</td></tr>
              );
            })}</tbody>
          </table></div>
        )}
      </section>
    </>
  );
}

function GroupTable({ name, rows }: { name: string; rows: InsightGroup[] }) {
  if (!rows.length) return <Empty what="No sessions in this period." />;
  return (
    <div class="table-wrap"><table class="data-table">
      <thead><tr><th>{name}</th><th class="num">Sessions</th><th class="num">First try</th><th class="num">Cycles</th>
        <th class="num">PRs merged</th><th class="num">Prompts</th><th class="num">Agent time</th><th class="num">Spend</th></tr></thead>
      <tbody>{rows.map(g => (
        <tr key={g.name}><td class="t">{g.name}</td><td class="num">{g.sessions}</td><td class="num">{pct(g.settled ? g.first_try_rate : null)}</td>
          <td class="num">{one(g.cycles)}</td><td class="num">{g.merged}</td><td class="num">{g.prompts}</td>
          <td class="num">{span(g.agent_seconds)}</td><td class="num">{cash(g.spend)}</td></tr>
      ))}</tbody>
    </table></div>
  );
}

// -------------------------------------------------------------------- cost

const SPEND: Series[] = [{ label: 'People', cls: 'c1' }, { label: 'Automations', cls: 'c2' }, { label: 'Agents', cls: 'c3' }];

export function Cost({ r, f, versus }: TabProps) {
  const t = r.totals, p = r.previous, u = t.usage;
  const tokens = u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens;
  const shown = f.scope === 'people' ? [0, 2] : f.scope === 'automations' ? [1, 2] : f.scope === 'agents' ? [2] : [0, 1, 2];
  return (
    <>
      <div class="tiles">
        <Tile label="Spend" value={cash(t.spend)} sub={`over ${r.days.length} days`} versus={versus}
          delta={percentDelta(t.spend, p.spend, 'neither')} spark={daily(r.days, d => d.spend)} />
        <Tile label="Per session" value={t.sessions ? cash(t.per_session) : '–'} sub={plural(t.sessions, 'session')} versus={versus}
          delta={percentDelta(t.per_session, p.per_session, 'down')} />
        <Tile label="Per merged PR" value={t.merged ? cash(t.cost_per_merged) : '–'} sub={`${t.merged} merged`} versus={versus}
          delta={amountDelta(t.cost_per_merged, p.cost_per_merged, cash, 'down', t.merged > 0 && p.merged > 0)} />
        {(f.scope === 'all' || f.scope === 'automations') && (
          <Tile label="Automations" value={cash(t.automation_spend)} sub={t.spend ? `${pct(t.automation_spend / t.spend)} of spend` : 'of spend'} versus={versus}
            delta={percentDelta(t.automation_spend, p.automation_spend, 'neither')} />
        )}
        <Tile label="Tokens" value={tokens ? compact(tokens) : '–'}
          sub={t.cache_hit_rate != null ? `${pct(t.cache_hit_rate)} from cache` : 'none recorded yet'} versus={versus}
          delta={percentDelta(tokens, p.usage.input_tokens + p.usage.output_tokens + p.usage.cache_read_tokens + p.usage.cache_write_tokens, 'neither')} />
      </div>
      <section class="card viz-card">
        <h2>Spend per day</h2>
        <p class="hint">By where the session started. Agents means sessions another session spawned.</p>
        <Columns label="Spend per day" series={shown.map(i => SPEND[i])} fmt={v => '$' + (v >= 100 ? Math.round(v) : +v.toFixed(2))}
          rows={r.days.map(d => ({ label: shortDate(d.date), values: shown.map(i => d.spend_by[i]) }))} />
      </section>
      <div class="grid2 pair">
        <section class="card">
          <h2>By model</h2>
          {r.by_model.length === 0 ? <Empty /> : (
            <div class="table-wrap"><table class="data-table">
              <thead><tr><th>Model</th><th class="num">Sessions</th><th class="num">Spend</th><th class="num">Per merged PR</th></tr></thead>
              <tbody>{r.by_model.map(g => (
                <tr key={g.name}><td>{g.name}</td><td class="num">{g.sessions}</td><td class="num">{cash(g.spend)}</td>
                  <td class="num">{g.merged ? cash(g.cost_per_merged) : '–'}</td></tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
        <section class="card">
          <h2>By team</h2>
          {r.by_team.length === 0 ? <Empty /> : (
            <div class="table-wrap"><table class="data-table">
              <thead><tr><th>Team</th><th class="num">Spend</th><th class="num">Share</th><th class="num">Change</th></tr></thead>
              <tbody>{r.by_team.map(g => {
                const d = percentDelta(g.spend, g.prev_spend ?? 0, 'neither');
                return (
                  <tr key={g.name}><td>{g.name}</td><td class="num">{cash(g.spend)}</td><td class="num">{pct(g.spend / Math.max(t.spend, 1e-9))}</td>
                    <td class="num" title={`compared with ${versus}`}>{d?.text ?? '–'}</td></tr>
                );
              })}</tbody>
            </table></div>
          )}
        </section>
      </div>
      <section class="card">
        <div class="card-head"><h2>Most expensive runs</h2><span class="push hint">A session with the sessions it spawned</span></div>
        <RunTable runs={r.runs} />
      </section>
      <section class="card">
        <h2>Tokens</h2>
        {!tokens ? <p class="hint">No token counts yet: they're recorded for prompts run from now on.</p> : (
          <>
            <dl class="kv">
              <dt>Input</dt><dd>{compact(u.input_tokens)}</dd>
              <dt>Output</dt><dd>{compact(u.output_tokens)}</dd>
              <dt>Cache reads</dt><dd>{compact(u.cache_read_tokens)}</dd>
              <dt>Cache writes</dt><dd>{compact(u.cache_write_tokens)}</dd>
              <dt>Cache hit rate</dt><dd>{pct(t.cache_hit_rate)} <span class="dim">cache reads ÷ (cache reads + input)</span></dd>
            </dl>
            <div class="table-wrap"><table class="data-table">
              <thead><tr><th>Model</th><th class="num">Input</th><th class="num">Output</th><th class="num">Cache reads</th><th class="num">Cache writes</th></tr></thead>
              <tbody>{r.by_model.map(g => (
                <tr key={g.name}><td>{g.name}</td><td class="num">{compact(g.usage.input_tokens)}</td><td class="num">{compact(g.usage.output_tokens)}</td>
                  <td class="num">{compact(g.usage.cache_read_tokens)}</td><td class="num">{compact(g.usage.cache_write_tokens)}</td></tr>
              ))}</tbody>
            </table></div>
          </>
        )}
      </section>
    </>
  );
}

const STATUS_TONE: Record<string, string> = { done: 'green', failed: 'red', stopped: 'amber', running: 'blue' };
const PR_TONE: Record<string, string> = { merged: 'green', open: 'blue', closed: 'red' };

/** How a run ended: its pull request if it opened one, else its status. */
function Outcome({ run }: { run: InsightRun }) {
  if (run.pr) return <span class={'chip ' + PR_TONE[run.pr]}>{run.pr === 'open' ? 'PR open' : run.pr === 'closed' ? 'PR closed' : 'merged'}</span>;
  return <span class={'chip ' + (STATUS_TONE[run.status] ?? '')}>{run.status}</span>;
}

function RunTable({ runs }: { runs: InsightRun[] }) {
  if (!runs.length) return <Empty />;
  return (
    <div class="table-wrap"><table class="data-table">
      <thead><tr><th>Session</th><th>Person</th><th class="num">Spawned</th><th class="num">Agent time</th><th>Outcome</th><th class="num">Spend</th></tr></thead>
      <tbody>{runs.map(x => (
        <tr key={x.id}><td class="t"><a href={'#/s/' + x.id}>{x.title || 'Untitled'}</a></td><td>{x.person}</td>
          <td class="num">{x.children}</td><td class="num">{span(x.agent_seconds)}</td><td><Outcome run={x} /></td><td class="num">{cash(x.spend)}</td></tr>
      ))}</tbody>
    </table></div>
  );
}

// ----------------------------------------------------------- pull requests

const MERGE_BUCKETS: Record<string, string> = { '1h': '<1h', '4h': '1–4h', '24h': '4–24h', '3d': '1–3d', '7d': '3–7d', more: '>7d' };

export function Pulls({ r, versus }: TabProps) {
  const t = r.totals, p = r.previous, pr = r.pull_requests;
  return (
    <>
      <p class="hint tab-note">Pull requests count from every source, whatever the scope.</p>
      <div class="tiles">
        <Tile label="Opened" value={String(t.opened)} sub={`${one(t.opened / r.days.length)} a day`} versus={versus}
          delta={percentDelta(t.opened, p.opened)} spark={daily(r.days, d => d.opened)} />
        <Tile label="Acceptance" value={pct(t.acceptance_rate)} sub={`${t.merged} merged · ${t.closed} closed`} versus={versus}
          delta={pointsDelta(t.acceptance_rate, p.acceptance_rate)} />
        <Tile label="Time to merge" value={span(t.median_to_merge_secs)} sub={t.p90_to_merge_secs ? `median · 90% within ${span(t.p90_to_merge_secs)}` : 'median'}
          versus={versus} delta={amountDelta(t.median_to_merge_secs, p.median_to_merge_secs, span, 'down', !!t.median_to_merge_secs && !!p.median_to_merge_secs)} />
        <Tile label="Open now" value={String(pr.open_now)} sub={pr.open_now ? `oldest ${span(pr.oldest_open_secs)}` : 'none waiting'} versus={versus} />
        <Tile label="Start to PR" value={span(t.median_to_open_secs)} sub="median, from the session's start" versus={versus}
          delta={amountDelta(t.median_to_open_secs, p.median_to_open_secs, span, 'down', !!t.median_to_open_secs && !!p.median_to_open_secs)} />
      </div>
      <section class="card viz-card">
        <h2>Opened and merged per day</h2>
        <Lines label="Pull requests opened and merged per day" series={[{ label: 'Opened', cls: 'c1' }, { label: 'Merged', cls: 'c2' }]}
          rows={r.days.map(d => ({ label: shortDate(d.date), values: [d.opened, d.merged] }))} />
      </section>
      <div class="grid2 pair">
        <section class="card">
          <h2>Where they stand</h2>
          <p class="hint">The window's pull requests, by their state now.</p>
          <StackBar parts={[
            { label: 'Merged', n: t.merged, cls: 's1' }, { label: 'Open', n: t.open - pr.draft, cls: 'c3' },
            { label: 'Draft', n: pr.draft, cls: 's0' }, { label: 'Closed', n: t.closed, cls: 's3' },
          ]} />
          {pr.by_source.length > 0 && (
            <div class="table-wrap"><table class="data-table">
              <thead><tr><th>Source</th><th class="num">Opened</th><th class="num">Merged</th><th class="num">Acceptance</th></tr></thead>
              <tbody>{pr.by_source.map(g => (
                <tr key={g.name}><td>{originName(g.name)}</td><td class="num">{g.opened}</td><td class="num">{g.merged}</td><td class="num">{pct(g.acceptance_rate)}</td></tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
        <section class="card viz-card">
          <h2>How long merging takes</h2>
          <p class="hint">Merged pull requests by the time from opening to merging.</p>
          <Columns label="Merged pull requests by time to merge" series={[{ label: 'Merged', cls: 'c1' }]} height={140} all
            rows={pr.to_merge.map(c => ({ label: MERGE_BUCKETS[c.label] ?? c.label, values: [c.n] }))} />
        </section>
      </div>
      <section class="card">
        <h2>By repository</h2>
        {pr.by_repo.length === 0 ? <Empty what="No pull requests in this period." /> : (
          <div class="table-wrap"><table class="data-table">
            <thead><tr><th>Repository</th><th class="num">Opened</th><th class="num">Merged</th><th class="num">Closed</th><th class="num">Acceptance</th>
              <th class="num">Median to merge</th><th class="num">Per merged</th></tr></thead>
            <tbody>{pr.by_repo.map(g => (
              <tr key={g.name}><td class="t">{g.name}</td><td class="num">{g.opened}</td><td class="num">{g.merged}</td><td class="num">{g.closed}</td>
                <td class="num">{pct(g.acceptance_rate)}</td><td class="num">{span(g.median_to_merge_secs)}</td>
                <td class="num">{g.merged ? cash(g.cost_per_merged) : '–'}</td></tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <section class="card">
        <div class="card-head"><h2>Waiting for review</h2><span class="push hint">Open now, oldest first, whenever they were opened</span></div>
        {pr.waiting.length === 0 ? <Empty what="No pull requests are open." /> : (
          <div class="table-wrap"><table class="data-table">
            <thead><tr><th>Pull request</th><th>Session by</th><th /><th class="num">Open for</th></tr></thead>
            <tbody>{pr.waiting.map(w => (
              <tr key={w.url}>
                <td class="t"><a href={w.url} target="_blank" rel="noreferrer"><span class="dim">{w.repo}#{w.number}</span> {w.title || 'Untitled'}</a></td>
                <td><a href={'#/s/' + w.session_id}>{w.person}</a></td>
                <td>{w.draft && <span class="chip">draft</span>}</td>
                <td class="num">{span((Date.now() - Date.parse(w.opened_at)) / 1000)}</td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
    </>
  );
}

// ------------------------------------------------------------------ people

type SortKey = 'sessions' | 'first_try_rate' | 'merged' | 'spend' | 'agent_seconds' | 'last_active';
const COLUMNS: [SortKey, string][] = [
  ['sessions', 'Sessions'], ['first_try_rate', 'First try'], ['merged', 'PRs merged'], ['agent_seconds', 'Agent time'], ['spend', 'Spend'], ['last_active', 'Last active'],
];

export function People({ r, f }: TabProps) {
  const [sort, setSort] = useState<SortKey>('sessions');
  const [filter, setFilter] = useState('');
  const [open, setOpen] = useState<InsightPerson>();
  const fn = r.funnel;
  const value = (p: InsightPerson) => (sort === 'last_active' ? Date.parse(p.last_active) : sort === 'first_try_rate' && !p.settled ? -1 : p[sort]);
  const rows = r.people.filter(p => p.name.toLowerCase().includes(filter.toLowerCase())).sort((a, b) => value(b) - value(a) || a.name.localeCompare(b.name));
  return (
    <>
      <div class="grid2 pair">
        <section class="card viz-card">
          <h2>Active people per week</h2>
          <p class="hint">People with at least one of the {SCOPE_NOTE[f.scope]}, the last twelve weeks.</p>
          <Lines label="Active people per week" height={120} series={[{ label: 'People', cls: 'c1' }]}
            rows={r.weeks.map(w => ({ label: shortDate(w.start), values: [w.people] }))} />
        </section>
        <section class="card">
          <h2>Getting started</h2>
          <p class="hint">Everyone invited in the last 90 days, by how far they've got.</p>
          {fn.invited === 0 ? <Empty what="Nobody was invited in the last 90 days." /> : (
            <>
              <Funnel steps={[{ label: 'Invited', n: fn.invited }, { label: 'Joined', n: fn.joined }, { label: 'First session', n: fn.started }, { label: 'First merged PR', n: fn.merged }]} />
              {fn.median_to_start_secs > 0 && <p class="hint">A first session came a median {span(fn.median_to_start_secs)} after joining.</p>}
            </>
          )}
        </section>
      </div>
      <section class="card">
        <div class="card-head">
          <h2>Everyone</h2>
          <span class="push"><input type="search" placeholder="Filter people" aria-label="Filter people" value={filter} onInput={e => setFilter(e.currentTarget.value)} /></span>
        </div>
        {rows.length === 0 ? <Empty what={r.people.length ? 'Nobody matches.' : 'No sessions in this period.'} /> : (
          <div class="table-wrap"><table class="data-table people-table">
            <thead><tr><th>Person</th><th>Daily sessions</th>
              {COLUMNS.map(([k, label]) => (
                <th key={k} class="num" aria-sort={sort === k ? 'descending' : undefined}>
                  <button type="button" class={'sort' + (sort === k ? ' on' : '')} onClick={() => setSort(k)}>{label}</button>
                </th>
              ))}</tr></thead>
            <tbody>{rows.map(p => (
              <tr key={p.account_id || p.name} class="clickable" onClick={() => setOpen(p)}>
                <td><button type="button" class="link" onClick={e => { e.stopPropagation(); setOpen(p); }}>{p.name}</button></td>
                <td><Spark values={p.daily} smooth={false} /></td>
                <td class="num">{p.sessions}</td><td class="num">{pct(p.settled ? p.first_try_rate : null)}</td><td class="num">{p.merged}</td>
                <td class="num">{span(p.agent_seconds)}</td><td class="num">{cash(p.spend)}</td><td class="num">{ago(p.last_active)}</td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      {open && <PersonSheet p={open} days={r.days.map(d => d.date)} close={() => setOpen(undefined)} />}
    </>
  );
}

function PersonSheet({ p, days, close }: { p: InsightPerson; days: string[]; close: () => void }) {
  useEffect(() => {
    const f = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    addEventListener('keydown', f);
    return () => removeEventListener('keydown', f);
  }, []);
  const max = Math.max(1, ...p.repos.map(x => x.n));
  return (
    <div class="sheet-bg" onPointerDown={e => { if (e.target === e.currentTarget) close(); }}>
      <aside class="sheet person-sheet" role="dialog" aria-label={p.name}>
        <div class="card-head"><h2>{p.name}</h2><button type="button" class="btn ghost push" aria-label="Close" onClick={close}>✕</button></div>
        <div class="tiles three">
          <div class="tile"><div class="tile-label">Sessions</div><div class="tile-value">{p.sessions}</div></div>
          <div class="tile"><div class="tile-label">First try</div><div class="tile-value">{pct(p.settled ? p.first_try_rate : null)}</div>
            <div class="tile-sub">{one(p.cycles)} cycles each</div></div>
          <div class="tile"><div class="tile-label">PRs merged</div><div class="tile-value">{p.merged}</div>
            <div class="tile-sub">{p.merged ? `${cash(p.cost_per_merged)} each` : cash(p.spend) + ' spent'}</div></div>
        </div>
        <div>
          <h3>Sessions per day</h3>
          <Columns label={`${p.name}'s sessions per day`} height={70} table={false} series={[{ label: 'Sessions', cls: 'c1' }]}
            rows={p.daily.map((n, i) => ({ label: shortDate(days[i]), values: [n] }))} />
        </div>
        {p.repos.length > 0 && (
          <div>
            <h3>Where they work</h3>
            <div class="viz bars">
              {p.repos.map(x => (
                <div key={x.label} class="bar-row wide">
                  <span class="bar-label">{x.label}</span>
                  <span class="bar-track"><span class="bar c1" style={{ width: `${(x.n / max) * 100}%` }} /><span class="bar-value">{x.n}</span></span>
                </div>
              ))}
            </div>
          </div>
        )}
        <div>
          <h3>Recent sessions</h3>
          <table class="data-table"><tbody>{p.recent.map(x => (
            <tr key={x.id}><td class="t"><a href={'#/s/' + x.id}>{x.title || 'Untitled'}</a></td><td><Outcome run={x} /></td><td class="num">{cash(x.spend)}</td></tr>
          ))}</tbody></table>
        </div>
        <ChartTable head={['Day', 'Sessions']} rows={p.daily.map((n, i) => [shortDate(days[i]), n])} />
      </aside>
    </div>
  );
}

// ------------------------------------------------------------- reliability

export function ReliabilityTab({ r, f, versus }: { r: Reliability; f: Filters; versus: string }) {
  return (
    <>
      <p class="hint tab-note">For admins. Prompts count with the {SCOPE_NOTE[f.scope]} that account for them; automations count whatever the scope.</p>
      <div class="tiles">
        <Tile label="Prompt failure rate" value={pct(r.failure_rate)} sub={`${r.failed} of ${plural(r.prompts, 'prompt')}`} versus={versus}
          delta={pointsDelta(r.failure_rate, r.prev_failure_rate, 'down')} spark={r.days.map(d => d.failed)} />
        <SpreadTile label="Prompt duration" s={r.duration} versus={versus} />
        <SpreadTile label="Queue wait" s={r.queue_wait} versus={versus} />
        <SpreadTile label="Sandbox start" s={r.sandbox_start} versus={versus} />
        <Tile label="Tool error rate" value={r.tool_calls ? pct(r.tool_errors / r.tool_calls) : '–'}
          sub={`${compact(r.tool_errors)} of ${compact(r.tool_calls)} calls`} versus={versus} />
      </div>
      <section class="card viz-card">
        <h2>Failed and stopped prompts per day</h2>
        <Columns label="Failed and stopped prompts per day" height={110} series={[{ label: 'Failed', cls: 's3' }, { label: 'Stopped', cls: 's2' }]}
          rows={r.days.map(d => ({ label: shortDate(d.date), values: [d.failed, d.stopped] }))} />
      </section>
      <div class="grid2 pair">
        <section class="card">
          <h2>Why prompts fail</h2>
          {r.failures.length === 0 ? <Empty what="No failures." /> : (
            <div class="table-wrap"><table class="data-table">
              <thead><tr><th>Reason</th><th>Trend</th><th class="num">Prompts</th></tr></thead>
              <tbody>{r.failures.map(x => (
                <tr key={x.label}><td class="t"><a href={'#/s/' + x.session_id} title={`Last ${ago(x.last_at)}: open that session`}>{x.label}</a></td>
                  <td><Spark values={x.daily} smooth={false} /></td><td class="num">{x.n}</td></tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
        <section class="card">
          <h2>Tools</h2>
          <p class="hint">The most called, with how often they returned an error.</p>
          {r.tools.length === 0 ? <Empty what="No tool calls." /> : (
            <div class="table-wrap"><table class="data-table">
              <thead><tr><th>Tool</th><th class="num">Calls</th><th class="num">Errors</th><th class="num">Rate</th></tr></thead>
              <tbody>{r.tools.map(x => (
                <tr key={x.name}><td class="t mono">{x.name}</td><td class="num">{x.calls.toLocaleString()}</td>
                  <td class="num">{x.errors.toLocaleString()}</td><td class="num">{pct(x.errors / Math.max(1, x.calls))}</td></tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
      </div>
      <section class="card">
        <div class="card-head"><h2>Automations</h2><a class="push small" href="#/automations">Manage →</a></div>
        {r.automations.length === 0 ? <Empty what="No automations ran." /> : (
          <div class="table-wrap"><table class="data-table">
            <thead><tr><th>Automation</th><th>Trigger</th><th class="num">Runs</th><th class="num">Succeeded</th><th class="num">PRs merged</th>
              <th class="num">Spend</th><th>Last run</th></tr></thead>
            <tbody>{r.automations.map(a => (
              <tr key={a.id}>
                <td class="t"><a href={'#/automations/' + a.id}>{a.name}</a></td><td><span class="chip">{a.trigger.replace(/_/g, ' ')}</span></td>
                <td class="num">{a.runs}</td><td class="num">{a.succeeded} <span class="dim">{a.runs ? pct(a.succeeded / a.runs) : ''}</span></td>
                <td class="num">{a.merged}</td><td class="num">{cash(a.spend)}</td>
                <td>{!a.enabled && a.failures >= 3 ? <span class="chip red" title={`Paused after ${a.failures} failures in a row`}>paused</span>
                  : !a.enabled ? <span class="chip">off</span>
                  : a.last_status ? <span class={'chip ' + (STATUS_TONE[a.last_status] ?? '')} title={a.last_at && ago(a.last_at)}>{a.last_status}</span> : <span class="dim">–</span>}</td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
    </>
  );
}

function SpreadTile({ label, s, versus }: { label: string; s: { n: number; median: number; p90: number; prev_median: number }; versus: string }) {
  return (
    <Tile label={label} value={span(s.median)} sub={s.n ? `median · 90% within ${span(s.p90)}` : 'none in this period'} versus={versus}
      delta={amountDelta(s.median, s.prev_median, span, 'down', !!s.median && !!s.prev_median)} />
  );
}

