import type { ComponentChildren } from 'preact';
import { useState } from 'preact/hooks';

// Charts for Insights: plain HTML and SVG on the app's tokens. Every chart
// has a legend when it shows more than one series, a tooltip on hover or
// focus, and its numbers as a table, so no value hangs on color alone.

/** Rounds up to 1, 2 or 5 times a power of ten. */
export function niceMax(v: number) {
  const p = 10 ** Math.floor(Math.log10(Math.max(v, 1)));
  return [1, 2, 5, 10].map(m => m * p).find(m => m >= v)!;
}

export const shortDate = (d: string) => new Date(d + 'T12:00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' });

/** A series: its label and color class (s0–s3 for outcomes, c1–c5 for categories). */
export type Series = { label: string; cls: string };
/** A labelled row of values, one per series. */
export type Row = { label: string; values: number[] };

/** Edge labels: the first, middle and last. */
const edges = (n: number) => new Set([0, Math.floor((n - 1) / 2), n - 1]);
const tipAt = (i: number, n: number) => (i < n * 0.15 ? ' at-left' : i > n * 0.85 ? ' at-right' : '');

function Legend({ series }: { series: Series[] }) {
  if (series.length < 2) return null; // the title names a single series
  return <div class="viz-legend">{series.map(s => <span key={s.label}><i class={'key ' + s.cls} />{s.label}</span>)}</div>;
}

function Axis({ ticks, max, fmt, height }: { ticks: number[]; max: number; fmt: (v: number) => string; height: number }) {
  return (
    <div class="viz-y" style={{ height }}>
      {ticks.map(t => <span key={t} style={{ bottom: `${(t / max) * 100}%` }}>{fmt(t)}</span>)}
    </div>
  );
}

/** Every value of a chart as a table, behind a disclosure. */
export function ChartTable({ head, rows }: { head: string[]; rows: (string | number)[][] }) {
  return (
    <details class="viz-table">
      <summary>Show as a table</summary>
      <table class="data-table">
        <thead><tr>{head.map((h, i) => <th key={h} class={i ? 'num' : ''}>{h}</th>)}</tr></thead>
        <tbody>{rows.map((r, i) => <tr key={i}>{r.map((v, j) => <td key={j} class={j ? 'num' : ''}>{v}</td>)}</tr>)}</tbody>
      </table>
    </details>
  );
}

/** Stacked columns: one per row, a segment per series. */
export function Columns({ rows, series, fmt = String, height = 160, all, extra, label, table = true }: {
  rows: Row[]; series: Series[]; fmt?: (v: number) => string; height?: number;
  /** Label every column, not just the edges. */
  all?: boolean;
  /** More lines for a column's tooltip. */
  extra?: (i: number) => ComponentChildren;
  label: string; table?: boolean;
}) {
  const [tip, setTip] = useState<number | null>(null);
  const totals = rows.map(r => r.values.reduce((a, b) => a + b, 0));
  const max = niceMax(Math.max(1, ...totals));
  const ticks = max % 2 === 0 ? [max, max / 2, 0] : [max, 0];
  const labelled = edges(rows.length);
  const r = tip == null ? undefined : rows[tip];
  return (
    <figure class="viz" aria-label={label}>
      <Legend series={series} />
      <div class="viz-body">
        <Axis ticks={ticks} max={max} fmt={fmt} height={height} />
        <div class="viz-plot" style={{ height }} onPointerLeave={() => setTip(null)}>
          {ticks.map(t => <div key={t} class="viz-grid" style={{ bottom: `${(t / max) * 100}%` }} />)}
          <div class="viz-cols">
            {rows.map((row, i) => (
              <div key={row.label} class={'viz-col' + (tip === i ? ' on' : '')} tabIndex={0} aria-label={`${row.label}: ${fmt(totals[i])}`}
                onPointerEnter={() => setTip(i)} onFocus={() => setTip(i)} onBlur={() => setTip(null)}>
                <div class="viz-stack" style={{ height: `${(totals[i] / max) * 100}%` }}>
                  {row.values.map((v, k) => v > 0 && <div key={k} class={'part ' + series[k].cls} style={{ flexGrow: v }} />)}
                </div>
              </div>
            ))}
          </div>
          {r && (
            <div class={'viz-tip' + tipAt(tip!, rows.length)} style={{ left: `${((tip! + 0.5) / rows.length) * 100}%` }} role="status">
              <div class="viz-tip-head">{r.label}</div>
              {series.map((s, k) => (
                <div key={s.label} class="viz-tip-row"><i class={'line ' + s.cls} /><b>{fmt(r.values[k])}</b><span>{s.label}</span></div>
              ))}
              {extra?.(tip!)}
            </div>
          )}
        </div>
      </div>
      <div class={'viz-x' + (all ? ' all' : '')}>
        {rows.map((row, i) => <span key={row.label}>{all || labelled.has(i) ? row.label : ''}</span>)}
      </div>
      {table && <ChartTable head={['', ...series.map(s => s.label)]} rows={rows.map(r => [r.label, ...r.values.map(fmt)])} />}
    </figure>
  );
}

/** Lines over time, one per series, with a crosshair. The first series gets a wash. */
export function Lines({ rows, series, height = 160, label }: { rows: Row[]; series: Series[]; height?: number; label: string }) {
  const [tip, setTip] = useState<number | null>(null);
  const W = 600, n = rows.length;
  const max = niceMax(Math.max(1, ...rows.flatMap(r => r.values)));
  const ticks = max % 2 === 0 ? [max, max / 2, 0] : [max, 0];
  const x = (i: number) => (n === 1 ? W / 2 : (i / (n - 1)) * W);
  const y = (v: number) => height - (v / max) * height;
  const path = (k: number) => rows.map((r, i) => `${i ? 'L' : 'M'}${x(i)},${y(r.values[k])}`).join('');
  const labelled = edges(n);
  const r = tip == null ? undefined : rows[tip];
  return (
    <figure class="viz" aria-label={label}>
      <Legend series={series} />
      <div class="viz-body">
        <Axis ticks={ticks} max={max} fmt={String} height={height} />
        <div class="viz-plot" style={{ height }} onPointerLeave={() => setTip(null)}>
          {ticks.map(t => <div key={t} class="viz-grid" style={{ bottom: `${(t / max) * 100}%` }} />)}
          <svg class="viz-lines" viewBox={`0 0 ${W} ${height}`} preserveAspectRatio="none" aria-hidden="true">
            {n > 1 && <path class={'wash ' + series[0].cls} d={`${path(0)}L${x(n - 1)},${height}L0,${height}Z`} />}
            {series.map((s, k) => <path key={s.label} class={'stroke ' + s.cls} d={path(k)} />)}
            {tip != null && <line class="crosshair" x1={x(tip)} x2={x(tip)} y1={0} y2={height} />}
          </svg>
          {r && series.map((s, k) => (
            <i key={s.label} class={'viz-dot ' + s.cls} style={{ left: `${(x(tip!) / W) * 100}%`, top: y(r.values[k]) }} />
          ))}
          <div class="viz-cols">
            {rows.map((row, i) => (
              <div key={row.label} class="viz-hit" tabIndex={0} aria-label={`${row.label}: ${row.values.join(', ')}`}
                onPointerEnter={() => setTip(i)} onFocus={() => setTip(i)} onBlur={() => setTip(null)} />
            ))}
          </div>
          {r && (
            <div class={'viz-tip' + tipAt(tip!, n)} style={{ left: `${(x(tip!) / W) * 100}%` }} role="status">
              <div class="viz-tip-head">{r.label}</div>
              {series.map((s, k) => (
                <div key={s.label} class="viz-tip-row"><i class={'line ' + s.cls} /><b>{r.values[k]}</b><span>{s.label}</span></div>
              ))}
            </div>
          )}
        </div>
      </div>
      <div class="viz-x">{rows.map((row, i) => <span key={row.label}>{labelled.has(i) ? row.label : ''}</span>)}</div>
      <ChartTable head={['', ...series.map(s => s.label)]} rows={rows.map(r => [r.label, ...r.values])} />
    </figure>
  );
}

/** A small trend line: the de-emphasis color, its last point in the accent. */
export function Spark({ values, smooth = true }: { values: number[]; smooth?: boolean }) {
  if (values.length < 2) return null;
  // A rolling week, so weekends don't drown the trend.
  const v = smooth ? values.map((_, i) => { const w = values.slice(Math.max(0, i - 6), i + 1); return w.reduce((a, b) => a + b, 0) / w.length; }) : values;
  const W = 52, H = 18, hi = Math.max(...v), lo = Math.min(...v), span = hi - lo || 1;
  const pts = v.map((x, i) => [(i / (v.length - 1)) * W, H - 2 - ((x - lo) / span) * (H - 4)]);
  const [lx, ly] = pts[pts.length - 1];
  return (
    <svg class="spark" viewBox={`0 0 ${W} ${H}`} aria-hidden="true">
      <polyline points={pts.map(p => p.join(',')).join(' ')} />
      <circle cx={lx} cy={ly} r="2.5" />
    </svg>
  );
}

/** Which way a change is good: up, down, or neither. */
export type Good = 'up' | 'down' | 'neither';
export type Delta = { text: string; dir: 'up' | 'down' | 'flat'; good: Good };

/** Change in percentage points between two rates. */
export function pointsDelta(cur: number | null | undefined, prev: number | null | undefined, good: Good = 'up'): Delta | undefined {
  if (cur == null || prev == null) return undefined;
  const d = Math.round((cur - prev) * 100);
  return { text: `${d > 0 ? '+' : d < 0 ? '−' : '±'}${Math.abs(d)} pts`, dir: d > 0 ? 'up' : d < 0 ? 'down' : 'flat', good };
}

/** Relative change between two amounts; none when there was nothing before. */
export function percentDelta(cur: number, prev: number, good: Good = 'up'): Delta | undefined {
  if (!prev) return undefined;
  const d = Math.round(((cur - prev) / prev) * 100);
  return { text: `${d > 0 ? '+' : d < 0 ? '−' : '±'}${Math.abs(d)}%`, dir: d > 0 ? 'up' : d < 0 ? 'down' : 'flat', good };
}

/** Change between two amounts, as fmt shows them. */
export function amountDelta(cur: number, prev: number, fmt: (v: number) => string, good: Good = 'up', has = prev !== 0): Delta | undefined {
  if (!has) return undefined;
  const d = cur - prev;
  if (fmt(Math.abs(d)) === fmt(0)) return { text: 'no change', dir: 'flat', good };
  return { text: `${d > 0 ? '+' : '−'}${fmt(Math.abs(d))}`, dir: d > 0 ? 'up' : 'down', good };
}

/** A headline number with what it's of, how it changed since the window before, and its trend. */
export function Tile({ label, value, sub, delta, spark, versus }: {
  label: string; value: string; sub?: ComponentChildren; delta?: Delta; spark?: number[]; versus: string;
}) {
  const tone = !delta || delta.dir === 'flat' || delta.good === 'neither' ? 'flat' : (delta.dir === delta.good) ? 'good' : 'bad';
  return (
    <div class="tile">
      <div class="tile-label">{label}</div>
      <div class="tile-value">{value}</div>
      {sub && <div class="tile-sub">{sub}</div>}
      {(delta || spark) && (
        <div class="tile-foot">
          {delta ? (
            <span class={'delta ' + tone} title={`${delta.text} compared with ${versus}`}>
              {delta.dir === 'up' ? '▲ ' : delta.dir === 'down' ? '▼ ' : ''}{delta.text}
              <span class="sr-only"> compared with {versus}</span>
            </span>
          ) : <span />}
          {spark && <Spark values={spark} />}
        </div>
      )}
    </div>
  );
}

/** Horizontal bars ranked by value, labels above. */
export function RankBars({ rows, fmt = String, onPick }: {
  rows: { label: string; value: number; sub?: string; key?: string }[]; fmt?: (v: number) => string; onPick?: (key: string) => void;
}) {
  if (!rows.length) return <p class="empty small">Nothing in this period.</p>;
  const max = Math.max(1, ...rows.map(r => r.value));
  return (
    <div class="rank">
      {rows.map(r => {
        const body = (
          <>
            <span class="rank-label">{r.label}</span>
            <span class="rank-value">{fmt(r.value)}{r.sub && <span class="dim">{r.sub}</span>}</span>
            <span class="rank-track"><i class="c1" style={{ width: `${(r.value / max) * 100}%` }} /></span>
          </>
        );
        return onPick
          ? <button type="button" key={r.label} class="rank-row" onClick={() => onPick(r.key ?? r.label)}>{body}</button>
          : <div key={r.label} class="rank-row">{body}</div>;
      })}
    </div>
  );
}

/** Parts of a whole in one bar, with a legend that carries the numbers. */
export function StackBar({ parts }: { parts: { label: string; n: number; cls: string }[] }) {
  const total = parts.reduce((a, p) => a + p.n, 0);
  return (
    <div class="viz">
      <div class="stackbar" role="img" aria-label={parts.map(p => `${p.label} ${p.n}`).join(', ')}>
        {total === 0 ? <i class="s0" style={{ flexGrow: 1 }} /> : parts.map(p => p.n > 0 && (
          <i key={p.label} class={p.cls} style={{ flexGrow: p.n }} title={`${p.label}: ${p.n} (${Math.round((p.n / total) * 100)}%)`} />
        ))}
      </div>
      <div class="viz-legend">{parts.map(p => <span key={p.label}><i class={'key ' + p.cls} />{p.label} {p.n}</span>)}</div>
    </div>
  );
}

/** One step of the sequential blue ramp per bin, lightest for none. */
const RAMP = ['heat-0', 'heat-1', 'heat-2', 'heat-3', 'heat-4', 'heat-5'];
const DAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

/** Counts by weekday and hour on a single-hue scale. */
export function Heatmap({ cells }: { cells: number[][] }) {
  const max = Math.max(1, ...cells.flat());
  const step = (v: number) => (v === 0 ? 0 : 1 + Math.min(RAMP.length - 2, Math.floor((v / max) * (RAMP.length - 1))));
  const hour = (h: number) => `${String(h).padStart(2, '0')}:00`;
  return (
    <figure class="viz" aria-label="Sessions by weekday and hour">
      <div class="heat-scale" aria-hidden="true">fewer {RAMP.slice(1).map(c => <i key={c} class={c} />)} more</div>
      <div class="heat">
        {cells.map((row, d) => [
          <span key={'d' + d} class="heat-day">{DAYS[d]}</span>,
          ...row.map((v, h) => <i key={d + '-' + h} class={RAMP[step(v)]} title={`${DAYS[d]} ${hour(h)}: ${v}`} />),
        ])}
        <span />
        {Array.from({ length: 24 }, (_, h) => <span key={'h' + h} class="heat-hour">{h % 6 === 0 ? hour(h).slice(0, 2) : ''}</span>)}
      </div>
      <ChartTable head={['Day', ...Array.from({ length: 24 }, (_, h) => String(h))]} rows={cells.map((row, d) => [DAYS[d], ...row])} />
    </figure>
  );
}

/** Ordered stages, each a share of the first. */
export function Funnel({ steps }: { steps: { label: string; n: number }[] }) {
  const first = Math.max(1, steps[0]?.n ?? 0);
  return (
    <div class="funnel">
      {steps.map((s, i) => (
        <div key={s.label} class="funnel-row">
          <span class="muted">{s.label}</span>
          <span class="rank-track"><i class={'ord-' + i} style={{ width: `${(s.n / first) * 100}%` }} /></span>
          <span class="funnel-value">{s.n}<span class="dim">{steps[0]?.n ? `${Math.round((s.n / first) * 100)}%` : ''}</span></span>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------- formats

export const pct = (x: number | null | undefined) => (x == null ? '–' : `${Math.round(x * 100)}%`);
export const one = (x: number) => (x ? x.toFixed(1) : '–');

/** A duration in seconds, at the precision it deserves. */
export function span(secs: number) {
  if (!secs || secs < 0) return '–';
  if (secs < 60) return `${Math.round(secs)}s`;
  if (secs < 3600) return `${Math.round(secs / 60)}m`;
  if (secs < 48 * 3600) return `${(secs / 3600).toFixed(secs < 36000 ? 1 : 0)}h`;
  return `${(secs / 86400).toFixed(1)} days`;
}

/** Dollars: cents below $1,000, whole dollars above. */
export function cash(n = 0) {
  if (n > 0 && n < 0.01) return '<$0.01';
  return n >= 1000 ? '$' + Math.round(n).toLocaleString() : '$' + n.toFixed(2);
}

/** A count, compact from a thousand up: 950, 12.9K, 4.2M. */
export function compact(n: number) {
  if (n < 1000) return String(Math.round(n));
  const [d, s] = n < 1e6 ? [1e3, 'K'] : n < 1e9 ? [1e6, 'M'] : [1e9, 'B'];
  return (n / d).toFixed(n / d < 10 ? 1 : 0).replace(/\.0$/, '') + s;
}
