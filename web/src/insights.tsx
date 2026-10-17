import { useEffect, useState } from 'preact/hooks';
import { get, type InsightDay, type InsightGroup, type Insights as Report } from './api';
import { useTeams } from './state';
import { Seg, errText, money, plural, readPref, writePref } from './util';

// How sessions go: quality first (cycles and the first-try rate), then pull
// requests, failures and usage. Every number covers the sessions created in
// the chosen window; quality covers the ones people started, once settled.

const DAYS_PREF = 'beethoven.insights.days';
const tz = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone; } catch { return ''; } })();

const pct = (x: number | null | undefined) => (x == null ? '–' : `${Math.round(x * 100)}%`);
const one = (x: number) => (x ? x.toFixed(1) : '–');

/** A duration in seconds, at the precision it deserves. */
export function span(secs: number) {
  if (!secs || secs < 0) return '–';
  if (secs < 60) return `${Math.round(secs)}s`;
  if (secs < 3600) return `${Math.round(secs / 60)}m`;
  if (secs < 48 * 3600) return `${(secs / 3600).toFixed(secs < 36000 ? 1 : 0)}h`;
  return `${(secs / 86400).toFixed(1)} days`;
}

const shortDate = (d: string) => new Date(d + 'T12:00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' });

export function Insights() {
  const [days, setDays] = useState<'7' | '30' | '90'>(() => readPref<'7' | '30' | '90'>(DAYS_PREF) ?? '30');
  const [team, setTeam] = useState('');
  const teams = useTeams();
  const [r, setR] = useState<Report>();
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  useEffect(() => {
    setLoading(true);
    get<Report>(`/api/insights?days=${days}&tz=${encodeURIComponent(tz)}&team=${team}`)
      .then(x => { setR(x); setErr(''); }, e => setErr(errText(e)))
      .finally(() => setLoading(false));
  }, [days, team]);
  const pick = (v: '7' | '30' | '90') => { setDays(v); writePref(DAYS_PREF, v); };

  return (
    <div class="page insights">
      <div class="page-head"><h1>Insights</h1></div>
      <div class="filters">
        <Seg value={days} set={pick} options={[['7', '7 days'], ['30', '30 days'], ['90', '90 days']]} />
        {!!teams?.length && (
          <select aria-label="Team" value={team} onChange={e => setTeam(e.currentTarget.value)}>
            <option value="">Every team</option>
            {teams.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
            <option value="workspace">Workspace: no team</option>
          </select>
        )}
      </div>
      {err && <p class="err" role="alert">{err}</p>}
      {!r ? !err && <p class="empty">Loading…</p> : (
        <div class={loading ? 'stale' : ''}>
          <p class="muted">
            Quality covers the sessions people started (not automations or agents), once they settle. A <b>cycle</b> is one
            prompt and its response; a session succeeds on the <b>first try</b> when one cycle was enough and its pull request
            wasn't closed without merging.
          </p>
          <div class="tiles">
            <Tile label="First-try rate" value={pct(r.settled ? r.first_try_rate : null)} sub={`of ${plural(r.settled, 'settled session')}`} />
            <Tile label="Cycles per session" value={one(r.cycles)} sub="prompts until done" />
            <Tile label="Success rate" value={pct(r.settled ? r.success_rate : null)} sub={`${plural(r.sessions, 'session')} started`} />
            <Tile label="Pull requests merged" value={String(r.pull_requests.merged)}
              sub={r.pull_requests.acceptance_rate == null ? 'none decided yet' : `${pct(r.pull_requests.acceptance_rate)} accepted`} />
            <Tile label="Spend" value={money(r.spend)}
              sub={r.pull_requests.merged ? `${money(r.pull_requests.cost_per_merged)} per merged PR` : 'no merged PRs yet'} />
          </div>

          <section class="card viz-card">
            <h2>How sessions went</h2>
            <p class="hint">Sessions people started each day, by outcome.</p>
            <OutcomeChart days={r.days} />
          </section>

          <div class="grid2 pair">
            <section class="card viz-card">
              <h2>Cycles to finish</h2>
              <p class="hint">Settled sessions by how many cycles they took.</p>
              <CycleBars counts={r.cycle_counts} />
            </section>
            <section class="card">
              <h2>Pull requests</h2>
              <dl class="kv">
                <dt>Opened</dt><dd>{r.pull_requests.opened}</dd>
                <dt>Merged</dt><dd>{r.pull_requests.merged}</dd>
                <dt>Closed unmerged</dt><dd>{r.pull_requests.closed}</dd>
                <dt>Still open</dt><dd>{r.pull_requests.open}</dd>
                <dt>Session start to PR</dt><dd><Median secs={r.pull_requests.median_to_open_secs} /></dd>
                <dt>PR to merge</dt><dd><Median secs={r.pull_requests.median_to_merge_secs} /></dd>
              </dl>
            </section>
          </div>

          <div class="grid2 pair">
            <section class="card">
              <h2>Failures</h2>
              <p class="hint">
                {plural(r.prompts, 'prompt')} ran: {r.failed} failed, {r.stopped} stopped. Agents worked {span(r.agent_seconds)} in all.
              </p>
              {r.failures.length === 0 ? <p class="empty small">No failures.</p> : (
                <table class="data-table">
                  <thead><tr><th>Reason</th><th class="num">Prompts</th></tr></thead>
                  <tbody>{r.failures.map(f => <tr key={f.label}><td>{f.label}</td><td class="num">{f.n}</td></tr>)}</tbody>
                </table>
              )}
            </section>
            <section class="card">
              <h2>Automations</h2>
              <dl class="kv">
                <dt>Runs</dt><dd>{r.automations.runs}</dd>
                <dt>Succeeded</dt><dd>{r.automations.succeeded} {r.automations.runs > 0 && <span class="dim">({pct(r.automations.succeeded / r.automations.runs)})</span>}</dd>
                <dt>Failed</dt><dd>{r.automations.failed}</dd>
                <dt>Spend</dt><dd>{money(r.automations.spend)}</dd>
              </dl>
            </section>
          </div>

          <Breakdown r={r} />
        </div>
      )}
    </div>
  );
}

const Median = ({ secs }: { secs: number }) => (secs > 0 ? <>{span(secs)} <span class="dim">median</span></> : <>–</>);

function Tile({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div class="tile">
      <div class="tile-label">{label}</div>
      <div class="tile-value">{value}</div>
      <div class="tile-sub">{sub}</div>
    </div>
  );
}

// ---------------------------------------------------------- outcome chart

const SERIES = [
  { key: 'first', label: 'First try', cls: 's1' },
  { key: 'more', label: 'After follow-ups', cls: 's2' },
  { key: 'bad', label: 'Unsuccessful', cls: 's3' },
  { key: 'busy', label: 'In progress', cls: 's0' },
] as const;

const parts = (d: InsightDay) => [d.first_try, d.more_cycles, d.unsuccessful, d.sessions - d.first_try - d.more_cycles - d.unsuccessful];

/** Rounds up to 1, 2 or 5 times a power of ten. */
function niceMax(v: number) {
  const p = 10 ** Math.floor(Math.log10(Math.max(v, 1)));
  return [1, 2, 5, 10].map(m => m * p).find(m => m >= v)!;
}

function OutcomeChart({ days }: { days: InsightDay[] }) {
  const [tip, setTip] = useState<number | null>(null);
  const max = niceMax(Math.max(1, ...days.map(d => d.sessions)));
  const ticks = max % 2 === 0 ? [max, max / 2, 0] : [max, 0];
  const labelled = new Set([0, Math.floor((days.length - 1) / 2), days.length - 1]);
  const d = tip == null ? undefined : days[tip];
  return (
    <figure class="viz" aria-label="Sessions per day by outcome">
      <div class="viz-legend">
        {SERIES.map(s => <span key={s.key}><i class={'key ' + s.cls} />{s.label}</span>)}
      </div>
      <div class="viz-body">
        <div class="viz-y">{ticks.map(t => <span key={t}>{t}</span>)}</div>
        <div class="viz-plot" onPointerLeave={() => setTip(null)}>
          {ticks.map(t => <div key={t} class="viz-grid" style={{ bottom: `${(t / max) * 100}%` }} />)}
          <div class="viz-cols">
            {days.map((day, i) => (
              <div key={day.date} class={'viz-col' + (tip === i ? ' on' : '')} tabIndex={0}
                aria-label={`${shortDate(day.date)}: ${day.sessions} sessions`}
                onPointerEnter={() => setTip(i)} onFocus={() => setTip(i)} onBlur={() => setTip(null)}>
                <div class="viz-stack" style={{ height: `${(day.sessions / max) * 100}%` }}>
                  {parts(day).map((v, k) => v > 0 && <div key={k} class={'seg ' + SERIES[k].cls} style={{ flexGrow: v }} />)}
                </div>
              </div>
            ))}
          </div>
          {d && (
            <div class={'viz-tip' + (tip! < days.length * 0.15 ? ' at-left' : tip! > days.length * 0.85 ? ' at-right' : '')}
              style={{ left: `${((tip! + 0.5) / days.length) * 100}%` }} role="status">
              <div class="viz-tip-head">{shortDate(d.date)}</div>
              {SERIES.map((s, k) => (
                <div key={s.key} class="viz-tip-row"><i class={'line ' + s.cls} /><b>{parts(d)[k]}</b><span>{s.label}</span></div>
              ))}
              <div class="viz-tip-row"><i class="line none" /><b>{d.merged}</b><span>PRs merged</span></div>
              <div class="viz-tip-row"><i class="line none" /><b>{money(d.spend)}</b><span>spent</span></div>
            </div>
          )}
        </div>
      </div>
      <div class="viz-x">
        {days.map((day, i) => <span key={day.date}>{labelled.has(i) ? shortDate(day.date) : ''}</span>)}
      </div>
      <details class="viz-table">
        <summary>Show as a table</summary>
        <table class="data-table">
          <thead>
            <tr><th>Day</th><th class="num">Sessions</th>{SERIES.map(s => <th key={s.key} class="num">{s.label}</th>)}
              <th class="num">PRs merged</th><th class="num">Spend</th></tr>
          </thead>
          <tbody>
            {days.map(day => (
              <tr key={day.date}>
                <td>{shortDate(day.date)}</td><td class="num">{day.sessions}</td>
                {parts(day).map((v, k) => <td key={k} class="num">{v}</td>)}
                <td class="num">{day.merged}</td><td class="num">{money(day.spend)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </figure>
  );
}

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

// -------------------------------------------------------------- breakdown

const SOURCES: Record<string, string> = { web: 'Web', cli: 'CLI', slack: 'Slack' };

function Breakdown({ r }: { r: Report }) {
  const [by, setBy] = useState<'source' | 'person' | 'repo'>('source');
  const rows: InsightGroup[] = by === 'source' ? r.by_source : by === 'person' ? r.by_person : r.by_repo;
  return (
    <section class="card">
      <div class="card-head">
        <h2>Breakdown</h2>
        <span class="push"><Seg value={by} set={setBy} options={[['source', 'Source'], ['person', 'Person'], ['repo', 'Workspace']]} /></span>
      </div>
      {rows.length === 0 ? <p class="empty small">No sessions in this period.</p> : (
        <table class="data-table">
          <thead>
            <tr><th>{by === 'source' ? 'Source' : by === 'person' ? 'Person' : 'Workspace'}</th><th class="num">Sessions</th>
              <th class="num">First try</th><th class="num">Cycles</th><th class="num">PRs merged</th><th class="num">Spend</th></tr>
          </thead>
          <tbody>
            {rows.map(g => (
              <tr key={g.name}>
                <td>{by === 'source' ? SOURCES[g.name] ?? g.name : g.name}</td>
                <td class="num">{g.sessions}</td>
                <td class="num">{pct(g.settled ? g.first_try_rate : null)}</td>
                <td class="num">{one(g.cycles)}</td>
                <td class="num">{g.merged}</td>
                <td class="num">{money(g.spend)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
