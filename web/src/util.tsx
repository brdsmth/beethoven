import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { get, type RepoRef } from './api';

export function useRoute(): string[] {
  const [hash, setHash] = useState(location.hash);
  useEffect(() => {
    const f = () => setHash(location.hash);
    addEventListener('hashchange', f);
    return () => removeEventListener('hashchange', f);
  }, []);
  return hash.replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent);
}

export const go = (path: string) => { location.hash = path; };

/** Re-renders the caller periodically so relative times stay fresh. */
export function useTick(ms = 30000) {
  const [, set] = useState(0);
  useEffect(() => { const t = setInterval(() => set(n => n + 1), ms); return () => clearInterval(t); }, [ms]);
}

export function ago(iso?: string) {
  if (!iso) return '';
  const s = (Date.now() - Date.parse(iso)) / 1000;
  if (s < 45) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  if (s < 86400 * 30) return `${Math.round(s / 86400)}d ago`;
  return new Date(iso).toLocaleDateString();
}

export const when = (iso: string) =>
  new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });

export const money = (n = 0) => (n > 0 && n < 0.01 ? '<$0.01' : '$' + n.toFixed(2));

export function dur(ms: number) {
  const s = Math.round(ms / 1000), m = Math.floor(s / 60);
  if (s < 60) return `${s}s`;
  return m < 60 ? `${m}m ${s % 60}s` : `${Math.floor(m / 60)}h ${m % 60}m`;
}

/** ⌘/Ctrl+Enter handler for textareas. */
export const modEnter = (fn: () => void) => (e: KeyboardEvent) => {
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); fn(); }
};

/** Form state with input binders. */
export function useForm<T extends Record<string, unknown>>(init: T) {
  const [f, setF] = useState(init);
  const set = (k: keyof T, v: unknown) => setF(p => ({ ...p, [k]: v }));
  const bind = (k: keyof T) => ({
    value: f[k] as string,
    onInput: (e: Event) => set(k, (e.currentTarget as HTMLInputElement).value),
  });
  const check = (k: keyof T) => ({
    checked: f[k] as boolean,
    onChange: (e: Event) => set(k, (e.currentTarget as HTMLInputElement).checked),
  });
  return { f, setF, bind, check };
}

// Toasts: transient messages from anywhere.
type Toast = { id: number; text: string; bad: boolean };
const toastFns = new Set<(t: Toast) => void>();
let toastId = 0;
export const toast = (text: string, bad = false) => toastFns.forEach(f => f({ id: ++toastId, text, bad }));
/** An error's message, without the API's generic prefix. */
export const errText = (e: unknown) => {
  const m = (e instanceof Error ? e.message : String(e)).replace(/^invalid request: /, '').replace(/^already exists: (.*)/, '$1 already exists');
  return m.charAt(0).toUpperCase() + m.slice(1);
};
export const fail = (e: unknown) => toast(errText(e), true);

export function Toasts() {
  const [list, setList] = useState<Toast[]>([]);
  useEffect(() => {
    const f = (t: Toast) => {
      setList(l => [...l, t]);
      setTimeout(() => setList(l => l.filter(x => x.id !== t.id)), t.bad ? 6000 : 2500);
    };
    toastFns.add(f);
    return () => { toastFns.delete(f); };
  }, []);
  return <div class="toasts" role="status">{list.map(t => <div key={t.id} class={'toast' + (t.bad ? ' bad' : '')}>{t.text}</div>)}</div>;
}

const tone: Record<string, string> = { running: 'blue', done: 'green', failed: 'red' };
export const Dot = ({ status }: { status: string }) => <span class={`dot ${tone[status] || 'gray'}`} title={status} />;
export const Chip = ({ status }: { status: string }) => <span class={`chip ${tone[status] || 'gray'}`}>{status}</span>;
export const Spinner = () => <span class="spin" aria-label="working" />;

/** Loads JSON from path (none while it is false); reload refetches. */
export function useLoad<T>(path: string | false): [T | undefined, () => void, (v: T) => void] {
  const [v, setV] = useState<T>();
  const latest = useRef(path);
  latest.current = path;
  const load = () => { const p = path; if (p) get<T>(p).then(x => { if (latest.current === p) setV(x); }, fail); };
  useEffect(() => { setV(undefined); load(); }, [path]);
  return [v, load, setV];
}

/** Per-browser preferences; storage may be unavailable. */
export function readPref<T>(key: string): T | undefined {
  try { return JSON.parse(localStorage.getItem(key) || 'null') ?? undefined; } catch { return undefined; }
}
export function writePref(key: string, v: unknown) {
  try { localStorage.setItem(key, JSON.stringify(v)); } catch { /* not remembered */ }
}

// Number inputs: "" means unset (0 on the wire).
export const num = (s: string) => (s.trim() ? Number(s) : 0);
export const str = (n?: number) => (n ? String(n) : '');

export const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;
export const repoSummary = (repos: RepoRef[]) =>
  repos.length ? repos[0].repo + (repos.length > 1 ? ` +${repos.length - 1}` : '') : '';
/** A session or automation's workspace in a few words. */
export const targetLabel = (t: { environment_id?: string; repos: RepoRef[] }, envs?: { id: string; name: string }[]) =>
  (t.environment_id && envs?.find(e => e.id === t.environment_id)?.name) || repoSummary(t.repos) || 'scratch';

export function Seg<T extends string>({ value, set, options }: { value: T; set: (v: T) => void; options: [T, string][] }) {
  return (
    <div class="seg">
      {options.map(([v, label]) => (
        <button type="button" key={v} class={v === value ? 'on' : ''} aria-pressed={v === value} onClick={() => set(v)}>{label}</button>
      ))}
    </div>
  );
}

export const MODELS = ['fable', 'opus', 'sonnet', 'haiku'];
let efforts = ['low', 'medium', 'high', 'xhigh', 'max'];
export const setEfforts = (list?: string[]) => { if (list?.length) efforts = list; };

/** A model: the default, a known alias, or any model ID. */
export function ModelField({ value, set, def }: { value: string; set: (v: string) => void; def: string }) {
  const [custom, setCustom] = useState(false);
  const isCustom = custom || (value !== '' && !MODELS.includes(value));
  return (
    <span class="stack">
      <select value={isCustom ? '*' : value} onChange={e => {
        const v = e.currentTarget.value;
        setCustom(v === '*');
        set(v === '*' ? (isCustom ? value : '') : v);
      }}>
        <option value="">{def}</option>
        {MODELS.map(m => <option key={m} value={m}>{m}</option>)}
        <option value="*">Custom…</option>
      </select>
      {isCustom && <input class="mono" placeholder="Model ID, e.g. claude-sonnet-4-5" spellcheck={false}
        value={value} onInput={e => set(e.currentTarget.value.trim())} />}
    </span>
  );
}

export const EffortSelect = ({ value, set, def }: { value: string; set: (v: string) => void; def: string }) => (
  <select value={value} onChange={e => set(e.currentTarget.value)}>
    <option value="">{def}</option>
    {efforts.map(x => <option key={x} value={x}>{x}</option>)}
  </select>
);

/** A destructive action that asks for a second click. */
export function ConfirmButton({ label = 'Delete', ask = label, note, small, onConfirm }: {
  label?: string; ask?: string; note?: ComponentChildren; small?: boolean; onConfirm: () => Promise<unknown>;
}) {
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const cls = 'btn' + (small ? ' sm' : '');
  if (!armed) return <button type="button" class={cls + ' danger'} onClick={() => setArmed(true)}>{label}</button>;
  return (
    <span class="confirm">
      {note && <span class="hint">{note}</span>}
      <button type="button" class={cls} onClick={() => setArmed(false)}>Cancel</button>
      <button type="button" class={cls + ' danger solid'} disabled={busy} autoFocus onClick={async () => {
        setBusy(true);
        try { await onConfirm(); setArmed(false); } catch (e) { fail(e); }
        setBusy(false);
      }}>{ask}</button>
    </span>
  );
}

// Settings page furniture.
export const Head = ({ title, children, actions }: { title: string; children?: ComponentChildren; actions?: ComponentChildren }) => (
  <div class="set-head">
    <div class="page-head"><h1>{title}</h1>{actions}</div>
    {children && <p class="muted">{children}</p>}
  </div>
);

export const Group = ({ title, hint, children }: { title: string; hint?: ComponentChildren; children: ComponentChildren }) => (
  <section class="set-group">
    <h2>{title}</h2>
    {hint && <p class="hint">{hint}</p>}
    {children}
  </section>
);

/** The save bar of a settings form: an inline error and one explicit Save. */
export const SaveBar = ({ dirty, busy, err, label = 'Save changes' }: { dirty: boolean; busy: boolean; err: string; label?: string }) => (
  <div class="save-bar">
    {err ? <span class="err" role="alert">{err}</span> : <span class="hint">{dirty ? 'Unsaved changes' : ''}</span>}
    <button class="btn primary" disabled={!dirty || busy}>{busy ? 'Saving…' : label}</button>
  </div>
);
