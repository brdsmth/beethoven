import { createContext } from 'preact';
import { useContext, useEffect, useState } from 'preact/hooks';
import { Icon, Select } from './components';
import { Markdown } from './markdown';
import { Seg, readPref, writePref } from './util';

// The viewer: a resizable panel beside a session's timeline that shows the
// files the agent shows with show_file. Each file is a snapshot taken out
// of the sandbox; showing the same path again adds a version.

/** A shown file, as the artifact event and the list endpoint describe it. */
export type Shown = { id: string; path: string; name: string; title?: string; content_type: string; size: number };

/** Opens a shown file in the session's viewer; provided by the session page. */
export const ViewerContext = createContext<(id: string) => void>(() => {});
export const useOpenFile = () => useContext(ViewerContext);

const WIDTH_PREF = 'beethoven.viewer.width';
const MIN_WIDTH = 320;
const baseType = (t: string) => t.split(';')[0].trim();
const fileURL = (session: string, id: string, download = false) =>
  `/api/sessions/${session}/artifacts/${id}${download ? '?download=1' : ''}`;

export const size = (n: number) =>
  n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`;

/** The panel: one tab per file path, the chosen version of it, and a handle
 * on its left edge to resize it. */
export function Viewer({ session, files, open, setOpen, close }: {
  session: string; files: Shown[]; open: string; setOpen: (id: string) => void; close: () => void;
}) {
  const [width, setWidth] = useState(() => readPref<number>(WIDTH_PREF) ?? 560);
  const current = files.find(f => f.id === open) ?? files[files.length - 1];
  // Tabs: each path once, most recently shown last.
  const paths = [...new Map(files.map(f => [f.path, f])).values()];
  const versions = files.filter(f => f.path === current?.path);
  const latest = (path: string) => files.filter(f => f.path === path).at(-1)!;

  const drag = (e: PointerEvent) => {
    e.preventDefault();
    const startX = e.clientX, startW = width;
    let w = startW;
    const move = (m: PointerEvent) => {
      w = Math.min(Math.max(MIN_WIDTH, startW + startX - m.clientX), innerWidth - 360);
      setWidth(w);
    };
    const up = () => {
      removeEventListener('pointermove', move);
      removeEventListener('pointerup', up);
      document.body.classList.remove('resizing');
      writePref(WIDTH_PREF, Math.round(w));
    };
    document.body.classList.add('resizing'); // iframes would swallow the pointer otherwise
    addEventListener('pointermove', move);
    addEventListener('pointerup', up);
  };
  const nudge = (e: KeyboardEvent) => {
    const step = e.key === 'ArrowLeft' ? 32 : e.key === 'ArrowRight' ? -32 : 0;
    if (!step) return;
    e.preventDefault();
    const w = Math.min(Math.max(MIN_WIDTH, width + step), innerWidth - 360);
    setWidth(w);
    writePref(WIDTH_PREF, w);
  };

  return (
    <aside class="viewer" style={{ width: `${width}px` }} aria-label="File viewer">
      <div class="viewer-handle" role="separator" aria-orientation="vertical" aria-label="Resize the viewer" tabIndex={0}
        onPointerDown={drag} onKeyDown={nudge} />
      <div class="viewer-tabs" role="tablist">
        {paths.map(f => (
          <button key={f.path} type="button" role="tab" aria-selected={f.path === current?.path} title={f.path}
            class={'viewer-tab' + (f.path === current?.path ? ' on' : '')} onClick={() => setOpen(latest(f.path).id)}>
            <span class="ellipsis">{f.name}</span>
          </button>
        ))}
        <button type="button" class="btn icon viewer-close" aria-label="Close the viewer" onClick={close}><Icon name="x" size={14} /></button>
      </div>
      {current ? (
        <>
          <div class="viewer-bar">
            <div class="viewer-what">
              <span class="ellipsis viewer-name">{current.title || current.name}</span>
              <span class="ellipsis dim mono viewer-path" title={current.path}>{current.path} · {size(current.size)}</span>
            </div>
            {versions.length > 1 && (
              <Select class="viewer-version" label="Version" value={current.id} set={setOpen}
                options={versions.map((v, i) => ({ value: v.id, label: `Version ${i + 1}${i === versions.length - 1 ? ' (latest)' : ''}` })).reverse()} />
            )}
            <a class="btn sm" href={fileURL(session, current.id)} target="_blank" rel="noopener noreferrer">Open ↗</a>
            <a class="btn sm" href={fileURL(session, current.id, true)} download={current.name}>Download</a>
          </div>
          <div class="viewer-body"><FileView key={current.id} session={session} f={current} /></div>
        </>
      ) : <p class="empty">Nothing shown yet.</p>}
    </aside>
  );
}

const DEVICES: [string, string][] = [['full', 'Full'], ['tablet', 'Tablet'], ['phone', 'Phone']];
const DEVICE_WIDTH: Record<string, string> = { full: '100%', tablet: '820px', phone: '390px' };

/** Shows one file by its type. Adding a type is one more case here. */
function FileView({ session, f }: { session: string; f: Shown }) {
  const url = fileURL(session, f.id);
  const t = baseType(f.content_type);
  if (t === 'text/html') return <HTMLView url={url} name={f.name} />;
  if (t.startsWith('image/')) return <ImageView url={url} name={f.name} />;
  if (t === 'application/pdf') return <iframe class="viewer-frame" src={url} title={f.name} />;
  if (t.startsWith('video/')) return <div class="viewer-center"><video src={url} controls /></div>;
  if (t.startsWith('audio/')) return <div class="viewer-center"><audio src={url} controls /></div>;
  if (t.startsWith('text/') || t === 'application/json') return <TextView url={url} type={t} />;
  return (
    <div class="viewer-center">
      <p class="muted">{f.name} can't be previewed here.</p>
      <a class="btn" href={fileURL(session, f.id, true)} download={f.name}>Download {size(f.size)}</a>
    </div>
  );
}

/** A mockup: run in a sandboxed frame with an opaque origin (the server's
 * Content-Security-Policy says the same), at a chosen device width. */
function HTMLView({ url, name }: { url: string; name: string }) {
  const [device, setDevice] = useState(() => readPref<string>('beethoven.viewer.device') ?? 'full');
  const pick = (d: string) => { setDevice(d); writePref('beethoven.viewer.device', d); };
  return (
    <div class="viewer-html">
      <div class="viewer-tools"><Seg value={device} set={pick} options={DEVICES} /></div>
      <div class="viewer-stage">
        <iframe class={'viewer-frame' + (device === 'full' ? '' : ' device')} style={{ width: DEVICE_WIDTH[device] }}
          src={url} title={name} sandbox="allow-scripts allow-forms allow-modals allow-popups" />
      </div>
    </div>
  );
}

/** An image, fitted to the panel; a click shows it at its actual size. */
function ImageView({ url, name }: { url: string; name: string }) {
  const [actual, setActual] = useState(false);
  return (
    <div class={'viewer-image' + (actual ? ' actual' : '')}>
      <img src={url} alt={name} title={actual ? 'Click to fit' : 'Click for actual size'} onClick={() => setActual(!actual)} />
    </div>
  );
}

/** Text: Markdown rendered, CSV as a table, JSON indented, anything else
 * with line numbers. */
function TextView({ url, type }: { url: string; type: string }) {
  const [text, setText] = useState<string>();
  const [err, setErr] = useState('');
  useEffect(() => {
    fetch(url).then(r => (r.ok ? r.text() : Promise.reject(new Error(r.statusText)))).then(setText, e => setErr(String(e.message || e)));
  }, [url]);
  if (err) return <p class="err viewer-pad">{err}</p>;
  if (text === undefined) return <p class="dim viewer-pad">Loading…</p>;
  if (type === 'text/markdown') return <div class="viewer-pad prose"><Markdown text={text} /></div>;
  if (type === 'text/csv') return <CSVTable text={text} />;
  let shown = text;
  if (type === 'application/json') {
    try { shown = JSON.stringify(JSON.parse(text), null, 2); } catch { /* show it as it is */ }
  }
  return <Lines text={shown} />;
}

function Lines({ text }: { text: string }) {
  const lines = text.replace(/\n$/, '').split('\n');
  return (
    <div class="viewer-code">
      <pre class="viewer-gutter" aria-hidden="true">{lines.map((_, i) => i + 1).join('\n')}</pre>
      <pre class="viewer-lines">{text}</pre>
    </div>
  );
}

const MAX_ROWS = 2000;

function CSVTable({ text }: { text: string }) {
  const rows = parseCSV(text);
  if (!rows.length) return <p class="dim viewer-pad">Empty.</p>;
  const [head, ...body] = rows;
  return (
    <div class="viewer-table">
      <table>
        <thead><tr>{head.map((c, i) => <th key={i}>{c}</th>)}</tr></thead>
        <tbody>{body.slice(0, MAX_ROWS).map((r, i) => <tr key={i}>{r.map((c, j) => <td key={j}>{c}</td>)}</tr>)}</tbody>
      </table>
      {body.length > MAX_ROWS && <p class="hint viewer-pad">Showing the first {MAX_ROWS} of {body.length} rows; download it for the rest.</p>}
    </div>
  );
}

/** Splits CSV into rows of cells, with quoted cells that hold commas,
 * quotes ("") and line breaks. */
export function parseCSV(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [], cell = '', quoted = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') { cell += '"'; i++; } else if (c === '"') quoted = false; else cell += c;
    } else if (c === '"') quoted = true;
    else if (c === ',') { row.push(cell); cell = ''; }
    else if (c === '\n' || c === '\r') {
      if (c === '\r' && text[i + 1] === '\n') i++;
      row.push(cell); rows.push(row); row = []; cell = '';
    } else cell += c;
  }
  if (cell || row.length) { row.push(cell); rows.push(row); }
  return rows;
}

/** A shown file on the timeline: opens it in the viewer. */
export function ShownCard({ f }: { f: Shown }) {
  const open = useOpenFile();
  return (
    <button type="button" class="card shown" onClick={() => open(f.id)}>
      <Icon name={baseType(f.content_type).startsWith('image/') ? 'image' : 'file'} size={16} />
      <span class="shown-what">
        <span class="ellipsis">{f.title || f.name}</span>
        <span class="ellipsis dim mono">{f.path}</span>
      </span>
      <span class="dim">Open</span>
    </button>
  );
}
