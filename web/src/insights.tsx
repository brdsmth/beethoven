import { useEffect, useRef, useState } from 'preact/hooks';
import { get, type InsightDay, type InsightScope, type Insights as Report, type Reliability } from './api';
import {
  Columns, RankBars, Tile, amountDelta, cash, one, pct, percentDelta, pointsDelta, shortDate, span, type Series,
} from './charts';
import { useDismiss } from './components';
import { Cost, People, Pulls, ReliabilityTab, Usage } from './insights-tabs';
import { useMe, useTeams } from './state';
import { Seg, errText, plural, readPref, useRoute, useTick, writePref } from './util';

// How sessions go, in tabs: an overview led by quality (cycles and the
// first-try rate), then usage, cost, pull requests, people and, for admins,
// reliability. Every number covers the sessions created in the chosen
// window, of the chosen scope, team and repository; the filters and the tab
// live in the URL so a link opens the same view.

export { span };

const DAYS_PREF = 'beethoven.insights.days';
const tz = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone; } catch { return ''; } })();

type Days = '7' | '30' | '90';
export type Filters = { days: Days; team: string; scope: InsightScope; repo: string };
export type TabProps = { r: Report; f: Filters; versus: string };

const TABS: [string, string][] = [
  ['', 'Overview'], ['usage', 'Usage'], ['cost', 'Cost'], ['pulls', 'Pull requests'], ['people', 'People'], ['reliability', 'Reliability'],
];
const SCOPES: [InsightScope, string][] = [['people', 'People'], ['automations', 'Automations'], ['agents', 'Agents'], ['all', 'All']];
export const SCOPE_NOTE: Record<InsightScope, string> = {
  people: 'sessions people started', automations: 'sessions automations started', agents: 'sessions agents spawned', all: 'every session',
};

function readFilters(): Filters {
  const q = new URLSearchParams(location.hash.split('?')[1] || '');
  const days = q.get('days');
  const scope = q.get('scope') as InsightScope;
  return {
    days: days === '7' || days === '30' || days === '90' ? days : readPref<Days>(DAYS_PREF) ?? '30',
    team: q.get('team') || '', repo: q.get('repo') || '',
    scope: SCOPES.some(([s]) => s === scope) ? scope : 'people',
  };
}

/** The query that names a view: only what differs from the defaults. */
function query(f: Filters, withTZ = false) {
  const q = new URLSearchParams();
  q.set('days', f.days);
  if (f.team) q.set('team', f.team);
  if (f.scope !== 'people') q.set('scope', f.scope);
  if (f.repo) q.set('repo', f.repo);
  if (withTZ) q.set('tz', tz);
  return q.toString();
}

export function Insights() {
  const route = useRoute();
  const me = useMe();
  const admin = !!me?.admin;
  const tab = TABS.some(([t]) => t === route[1]) && (route[1] !== 'reliability' || admin) ? route[1] ?? '' : '';
  const [f, setF] = useState<Filters>(readFilters);
  const teams = useTeams();
  const [r, setR] = useState<Report>();
  const [rel, setRel] = useState<Reliability>();
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  const [at, setAt] = useState<number>(0);
  const [nonce, setNonce] = useState(0);
  useTick(30000);

  const path = (t: string) => `#/insights${t ? '/' + t : ''}?${query(f)}`;
  useEffect(() => { history.replaceState(null, '', path(tab)); }, [f, tab]);
  // A link followed from here names its own filters.
  useEffect(() => {
    const follow = () => setF(p => { const n = readFilters(); return query(n) === query(p) ? p : n; });
    addEventListener('hashchange', follow);
    return () => removeEventListener('hashchange', follow);
  }, []);
  useEffect(() => {
    setLoading(true);
    const q = query(f, true);
    Promise.all([
      get<Report>(`/api/insights?${q}`),
      tab === 'reliability' ? get<Reliability>(`/api/insights/reliability?${q}`) : Promise.resolve(undefined),
    ]).then(([x, y]) => { setR(x); if (y) setRel(y); setErr(''); setAt(Date.now()); }, e => setErr(errText(e)))
      .finally(() => setLoading(false));
  }, [f, tab === 'reliability', nonce]);
  // Coming back to the page after a while brings the numbers up to date.
  useEffect(() => {
    const f = () => { if (document.visibilityState === 'visible' && Date.now() - at > 60000) setNonce(n => n + 1); };
    addEventListener('visibilitychange', f);
    return () => removeEventListener('visibilitychange', f);
  }, [at]);

  const set = (patch: Partial<Filters>) => {
    if (patch.days) writePref(DAYS_PREF, patch.days);
    setF(p => ({ ...p, ...patch }));
  };
  const repos = r ? (f.repo && !r.repos.includes(f.repo) ? [f.repo, ...r.repos] : r.repos) : f.repo ? [f.repo] : [];
  const versus = `the ${f.days} days before`;

  return (
    <div class="page insights">
      <div class="page-head">
        <h1>Insights</h1>
        <span class="insights-meta">
          {at > 0 && <span class="dim">Updated {Date.now() - at < 60000 ? 'just now' : `${Math.round((Date.now() - at) / 60000)}m ago`}</span>}
          <About />
        </span>
      </div>
      <nav class="tabs" aria-label="Insights">
        {TABS.filter(([t]) => t !== 'reliability' || admin).map(([t, label]) => (
          <a key={t} href={path(t)} class={'tab' + (t === tab ? ' on' : '')} aria-current={t === tab ? 'page' : undefined}>{label}</a>
        ))}
      </nav>
      <div class="filters">
        <Seg value={f.days} set={days => set({ days })} options={[['7', '7 days'], ['30', '30 days'], ['90', '90 days']]} />
        {!!teams?.length && (
          <select aria-label="Team" value={f.team} onChange={e => set({ team: e.currentTarget.value })}>
            <option value="">Every team</option>
            {teams.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
            <option value="workspace">Workspace: no team</option>
          </select>
        )}
        <div class={'scope' + (tab === 'pulls' ? ' off' : '')} title={tab === 'pulls' ? 'Pull requests count from every source' : undefined}>
          <Seg value={f.scope} set={scope => set({ scope })} options={SCOPES} />
        </div>
        <select aria-label="Repository" value={f.repo} onChange={e => set({ repo: e.currentTarget.value })}>
          <option value="">Every repository</option>
          {repos.map(x => <option key={x} value={x}>{x}</option>)}
        </select>
      </div>
      {err && <p class="err" role="alert">{err}</p>}
      {!r ? !err && <p class="empty">Loading…</p> : (
        <div class={'tab-panel' + (loading ? ' stale' : '')}>
          {tab === '' && <Overview r={r} f={f} versus={versus} />}
          {tab === 'usage' && <Usage r={r} f={f} versus={versus} />}
          {tab === 'cost' && <Cost r={r} f={f} versus={versus} />}
          {tab === 'pulls' && <Pulls r={r} f={f} versus={versus} />}
          {tab === 'people' && <People r={r} f={f} versus={versus} />}
          {tab === 'reliability' && (rel ? <ReliabilityTab r={rel} f={f} versus={versus} /> : <p class="empty">Loading…</p>)}
        </div>
      )}
    </div>
  );
}

/** What to know about the numbers. */
function About() {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLSpanElement>(null);
  useDismiss(open, () => setOpen(false), ref);
  return (
    <span class="about" ref={ref}>
      <button type="button" class="btn sm ghost" aria-expanded={open} onClick={() => setOpen(!open)}>About this data</button>
      {open && (
        <div class="popover about-pop" role="dialog" aria-label="About this data">
          <ul>
            <li>Numbers cover the sessions <b>created</b> in the window, by day in your timezone, compared with the window before.</li>
            <li>The scope picks the sessions. Work a session's agent spawned counts for the session in scope above it.</li>
            <li>Quality counts sessions once they settle: they ran and have nothing queued or running.</li>
            <li>Pull requests count from every source, whatever the scope, and refresh from GitHub every 10 minutes.</li>
            <li>You only see sessions you may read; other people's private sessions are left out.</li>
            <li>Spend and tokens are what the agent reported. Tokens count from prompts run since they were first recorded.</li>
          </ul>
        </div>
      )}
    </span>
  );
}

// ------------------------------------------------------------- daily rates

const ratio = (n: number, of: number) => (of ? n / of : 0);
/** A measure per day, for a sparkline. */
export const daily = (days: InsightDay[], f: (d: InsightDay) => number) => days.map(f);

// ---------------------------------------------------------------- overview

const OUTCOMES: Series[] = [
  { label: 'First try', cls: 's1' }, { label: 'After follow-ups', cls: 's2' }, { label: 'Unsuccessful', cls: 's3' }, { label: 'In progress', cls: 's0' },
];

function Overview({ r, f, versus }: TabProps) {
  const t = r.totals, p = r.previous, days = r.days;
  const hasPrev = p.sessions > 0;
  return (
    <>
      <div class="section-cap">Quality · {SCOPE_NOTE[f.scope]}</div>
      <div class="tiles">
        <Tile label="First-try rate" value={pct(t.settled ? t.first_try_rate : null)} sub={`of ${plural(t.settled, 'settled session')}`}
          delta={pointsDelta(t.settled ? t.first_try_rate : null, p.settled ? p.first_try_rate : null)} versus={versus}
          spark={daily(days, d => ratio(d.first_try, d.settled))} />
        <Tile label="Cycles per session" value={one(t.cycles)} sub="prompts until done" versus={versus}
          delta={amountDelta(t.cycles, p.cycles, one, 'down', t.settled > 0 && p.settled > 0)} spark={daily(days, d => ratio(d.cycles, d.settled))} />
        <Tile label="Success rate" value={pct(t.settled ? t.success_rate : null)} sub="of settled sessions" versus={versus}
          delta={pointsDelta(t.settled ? t.success_rate : null, p.settled ? p.success_rate : null)}
          spark={daily(days, d => ratio(d.first_try + d.more_cycles, d.settled))} />
        <Tile label="Active people" value={String(t.people)} sub={t.people ? `${one(t.sessions / t.people)} sessions each` : 'nobody yet'}
          versus={versus} delta={amountDelta(t.people, p.people, String, 'up', hasPrev)} spark={daily(days, d => d.people)} />
      </div>
      <div class="section-cap">Throughput</div>
      <div class="tiles">
        <Tile label="Sessions" value={String(t.sessions)} sub={`${one(t.sessions / days.length)} a day`} versus={versus}
          delta={percentDelta(t.sessions, p.sessions)} spark={daily(days, d => d.sessions)} />
        <Tile label="PRs merged" value={String(t.merged)} sub={t.median_to_merge_secs ? `${span(t.median_to_merge_secs)} median to merge` : 'from every source'}
          versus={versus} delta={percentDelta(t.merged, p.merged)} spark={daily(days, d => d.merged)} />
        <Tile label="Spend" value={cash(t.spend)} sub={t.sessions ? `${cash(t.per_session)} per session` : 'nothing yet'} versus={versus}
          delta={percentDelta(t.spend, p.spend, 'neither')} spark={daily(days, d => d.spend)} />
        <Tile label="Cost per merged PR" value={t.merged ? cash(t.cost_per_merged) : '–'}
          sub={t.merged ? `${plural(t.merged, 'merged pull request')}` : 'no merged PRs yet'} versus={versus}
          delta={amountDelta(t.cost_per_merged, p.cost_per_merged, cash, 'down', t.merged > 0 && p.merged > 0)} />
      </div>

      <section class="card viz-card">
        <h2>How sessions went</h2>
        <p class="hint">{SCOPE_NOTE[f.scope].replace(/^./, c => c.toUpperCase())} each day, by outcome.</p>
        <Columns label="Sessions per day by outcome" series={OUTCOMES}
          rows={days.map(d => ({ label: shortDate(d.date), values: [d.first_try, d.more_cycles, d.unsuccessful, d.sessions - d.first_try - d.more_cycles - d.unsuccessful] }))}
          extra={i => <>
            <div class="viz-tip-row"><i class="line none" /><b>{days[i].merged}</b><span>PRs merged</span></div>
            <div class="viz-tip-row"><i class="line none" /><b>{cash(days[i].spend)}</b><span>spent</span></div>
          </>} />
      </section>

      <div class="grid2 pair">
        <section class="card viz-card">
          <h2>Cycles to finish</h2>
          <p class="hint">Settled sessions by how many cycles they took.</p>
          <CycleBars counts={r.cycle_counts} />
        </section>
        <section class="card">
          <div class="card-head"><h2>Pull requests</h2><a class="push small" href={`#/insights/pulls?days=${f.days}`}>More →</a></div>
          <dl class="kv">
            <dt>Opened</dt><dd>{t.opened}</dd>
            <dt>Merged</dt><dd>{t.merged} {t.acceptance_rate != null && <span class="dim">({pct(t.acceptance_rate)} accepted)</span>}</dd>
            <dt>Closed unmerged</dt><dd>{t.closed}</dd>
            <dt>Still open</dt><dd>{t.open}</dd>
            <dt>Session start to PR</dt><dd><Median secs={t.median_to_open_secs} /></dd>
            <dt>PR to merge</dt><dd><Median secs={t.median_to_merge_secs} /></dd>
          </dl>
        </section>
      </div>

      <div class="grid3">
        <section class="card">
          <div class="card-head"><h2>Repositories</h2><TabLink tab="usage" f={f} /></div>
          <p class="hint">By sessions, with spend</p>
          <RankBars rows={r.by_repo.slice(0, 5).map(g => ({ label: g.name, value: g.sessions, sub: cash(g.spend) }))} />
        </section>
        <section class="card">
          <div class="card-head"><h2>Models</h2><TabLink tab="cost" f={f} /></div>
          <p class="hint">By spend, with cost per session</p>
          <RankBars fmt={cash} rows={r.by_model.slice(0, 5).map(g => ({ label: g.name, value: g.spend, sub: `${cash(g.spend / Math.max(1, g.sessions))} each` }))} />
        </section>
        <section class="card">
          <div class="card-head"><h2>People</h2><TabLink tab="people" f={f} /></div>
          <p class="hint">By sessions, with first-try rate</p>
          <RankBars rows={r.people.slice(0, 5).map(g => ({ label: g.name, value: g.sessions, sub: pct(g.settled ? g.first_try_rate : null) }))} />
        </section>
      </div>
    </>
  );
}

const TabLink = ({ tab, f }: { tab: string; f: Filters }) => <a class="push small" href={`#/insights/${tab}?${query(f)}`}>All →</a>;

export const Median = ({ secs }: { secs: number }) => (secs > 0 ? <>{span(secs)} <span class="dim">median</span></> : <>–</>);

function CycleBars({ counts }: { counts: { label: string; n: number }[] }) {
  const max = Math.max(1, ...counts.map(c => c.n));
  const name = (l: string) => (l === '1' ? '1 cycle' : l === '4+' ? '4 or more' : `${l} cycles`);
  return (
    <div class="viz bars">
      {counts.map(c => (
        <div key={c.label} class="bar-row">
          <span class="bar-label">{name(c.label)}</span>
          <span class="bar-track">
            {c.n > 0 && <span class="bar s1" style={{ width: `${(c.n / max) * 100}%` }} />}
            <span class="bar-value">{c.n}</span>
          </span>
        </div>
      ))}
    </div>
  );
}

