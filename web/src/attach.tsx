import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { attachmentURL, type Attachment } from './api';
import { toast } from './util';

// What the server accepts with one prompt.
const MAX_FILES = 10, MAX_BYTES = 50 << 20;
const LIMITS = `A prompt can have up to ${MAX_FILES} attached files, ${MAX_BYTES >> 20} MB in all.`;

export function bytes(n: number) {
  if (n < 1024) return `${n} B`;
  if (n < 1 << 20) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1 << 20)).toFixed(1)} MB`;
}

/** Files waiting to go with a prompt: picked, pasted, or dropped anywhere
 * on the page; `over` is true while files are dragged over it. */
export function useFiles() {
  const [files, setFiles] = useState<File[]>([]);
  const [over, setOver] = useState(false);
  const cur = useRef(files);
  const set = (next: File[]) => { cur.current = next; setFiles(next); };
  const add = (incoming: File[]) => {
    const next = [...cur.current, ...incoming];
    if (next.length > MAX_FILES || next.reduce((t, f) => t + f.size, 0) > MAX_BYTES) toast(LIMITS, true);
    else if (incoming.length) set(next);
  };
  useEffect(() => {
    let depth = 0; // dragenter and dragleave pair up, element by element
    const hasFiles = (e: DragEvent) => !!e.dataTransfer?.types.includes('Files');
    const on: Record<string, (e: DragEvent) => void> = {
      dragenter: e => { if (hasFiles(e) && depth++ === 0) setOver(true); },
      dragleave: e => { if (hasFiles(e) && (depth = Math.max(0, depth - 1)) === 0) setOver(false); },
      dragover: e => { if (hasFiles(e)) e.preventDefault(); },
      drop: e => {
        if (!hasFiles(e)) return;
        e.preventDefault(); // else the browser leaves the page to show the file
        depth = 0;
        setOver(false);
        add([...e.dataTransfer!.files]);
      },
    };
    for (const k in on) window.addEventListener(k, on[k] as EventListener);
    return () => { for (const k in on) window.removeEventListener(k, on[k] as EventListener); };
  }, []);
  return {
    files, add,
    remove: (f: File) => set(cur.current.filter(x => x !== f)),
    clear: () => set([]),
    /** For a text field: pasting an image (a screenshot) attaches it. */
    onPaste: (e: ClipboardEvent) => {
      const pasted = [...(e.clipboardData?.files || [])];
      if (!pasted.length) return;
      if (!e.clipboardData!.types.includes('text/plain')) e.preventDefault();
      add(pasted);
    },
    over,
  };
}

const Clip = () => (
  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
    stroke-linejoin="round" aria-hidden="true">
    <path d="m21.4 11.1-9.2 9.2a6 6 0 0 1-8.5-8.5l9.2-9.2a4 4 0 0 1 5.7 5.7l-9.2 9.2a2 2 0 0 1-2.8-2.8l8.5-8.5" />
  </svg>
);

const Doc = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
    stroke-linejoin="round" aria-hidden="true">
    <path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z" /><path d="M14 3v6h6" />
  </svg>
);

/** A button that picks files to attach; with a label, a full button. */
export function AttachButton({ add, label }: { add: (f: File[]) => void; label?: string }) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <>
      <input ref={input} type="file" multiple hidden onChange={e => {
        add([...(e.currentTarget.files || [])]);
        e.currentTarget.value = '';
      }} />
      <button type="button" class={label ? 'btn' : 'btn icon'} title="Attach files (or paste or drop them)"
        aria-label={label ? undefined : 'Attach files'} onClick={() => input.current?.click()}>
        <Clip />{label}
      </button>
    </>
  );
}

const keys = new WeakMap<File, number>();
let lastKey = 0;
const keyOf = (f: File) => keys.get(f) ?? (keys.set(f, ++lastKey), lastKey);

/** Files waiting to be sent, each removable. */
export function PendingFiles({ files, remove }: { files: File[]; remove: (f: File) => void }) {
  if (!files.length) return null;
  return <div class="files">{files.map(f => <Pending key={keyOf(f)} f={f} remove={() => remove(f)} />)}</div>;
}

function Pending({ f, remove }: { f: File; remove: () => void }) {
  const url = useMemo(() => (f.type.startsWith('image/') ? URL.createObjectURL(f) : ''), [f]);
  useEffect(() => () => { if (url) URL.revokeObjectURL(url); }, [url]);
  return (
    <span class="file" title={`${f.name} · ${bytes(f.size)}`}>
      {url ? <img src={url} alt="" /> : <Doc />}
      <span class="file-name">{f.name}</span>
      <span class="dim">{bytes(f.size)}</span>
      <button type="button" class="x" aria-label={`Remove ${f.name}`} onClick={remove}>✕</button>
    </span>
  );
}

// Images the server lets a browser show; other files download.
const SHOWN = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
const isImage = (a: Attachment) => SHOWN.includes(a.content_type.split(';')[0].trim());

/** A prompt's attachments: images as thumbnails, other files (and images
 * that fail to load) as links. */
export function Attachments({ session, files }: { session: string; files: Attachment[] }) {
  const [broken, setBroken] = useState<Record<string, boolean>>({});
  const shown = (a: Attachment) => isImage(a) && !broken[a.id];
  const images = files.filter(shown), others = files.filter(a => !shown(a));
  return (
    <div class="files">
      {images.map(a => (
        <a key={a.id} class="thumb" href={attachmentURL(session, a)} target="_blank" rel="noopener" title={`${a.name} · ${bytes(a.size)}`}>
          <img src={attachmentURL(session, a)} alt={a.name} loading="lazy" onError={() => setBroken(b => ({ ...b, [a.id]: true }))} />
        </a>
      ))}
      {others.map(a => (
        <a key={a.id} class="file" href={attachmentURL(session, a)} target="_blank" rel="noopener" title={a.content_type}>
          <Doc /><span class="file-name">{a.name}</span><span class="dim">{bytes(a.size)}</span>
        </a>
      ))}
    </div>
  );
}
