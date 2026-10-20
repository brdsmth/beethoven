// Shared components, styled like the rest of the app. They import nothing
// from the pages, so any page or helper can use them.
import type { RefObject } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';

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
  flag: 'M3.5 14.5v-12M3.5 2.5h8l-2 3 2 3h-8',
  down: 'm4.5 6.5 3.5 3.5 3.5-3.5',
  up: 'M8 13V3M3.5 7.5 8 3l4.5 4.5',
  gauge: 'M2.5 12a5.5 5.5 0 1 1 11 0M8 12l2.5-3',
  pen: 'M10.5 2.5l3 3L6 13H3v-3z',
  x: 'M4 4l8 8M12 4l-8 8',
  queue: 'M2.5 4h11M2.5 8h7M2.5 12h5M12 9.5v5M9.5 12h5',
  file: 'M4 1.5h5.5L12.5 4.5v10H4zM9.5 1.5v3h3',
  image: 'M2.5 3h11v10h-11zM2.5 11l3.5-3.5 3 3 2-2 2.5 2.5M10.5 5.5a1 1 0 1 1 0 2 1 1 0 0 1 0-2z',
  panel: 'M2.5 3h11v10h-11zM9.5 3v10',
  sidebar: 'M2.5 3h11v10h-11zM6.5 3v10',
  pr: 'M4.5 2.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zM4.5 10.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zM11.5 10.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zM4.5 5.5v5M11.5 10.5V6.5a2 2 0 0 0-2-2H7.5M9 3l-1.5 1.5L9 6',
};

export const Icon = ({ name, size = 16 }: { name: string; size?: number }) => (
  <svg class="ico" width={size} height={size} viewBox="0 0 16 16" aria-hidden="true"><path d={ICONS[name]} /></svg>
);

/** Closes a popup on a press outside ref or on Escape. */
export function useDismiss(open: boolean, close: () => void, ref: RefObject<HTMLElement>) {
  useEffect(() => {
    if (!open) return;
    const out = (e: Event) => { if (!ref.current?.contains(e.target as Node)) close(); };
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    addEventListener('pointerdown', out);
    addEventListener('keydown', esc);
    return () => { removeEventListener('pointerdown', out); removeEventListener('keydown', esc); };
  }, [open]);
}

export type Option<T extends string = string> = { value: T; label: string; hint?: string };

/** A dropdown in place of a native <select>: a field-styled button that
 * opens the app's menu, with keyboard support (arrows, Home/End, Enter,
 * Escape, and typing a letter jumps to the next option starting with it). */
export function Select<T extends string = string>({ value, set, options, placeholder = 'Choose…', label, disabled, class: cls }: {
  value: T | ''; set: (v: T) => void; options: Option<T>[]; placeholder?: string; label?: string; disabled?: boolean; class?: string;
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const ref = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  useDismiss(open, () => setOpen(false), ref);
  const current = options.find(o => o.value === value);

  const show = () => { setActive(Math.max(0, options.findIndex(o => o.value === value))); setOpen(true); };
  const pick = (o: Option<T>) => { set(o.value); setOpen(false); ref.current?.querySelector('button')?.focus(); };

  useEffect(() => {
    if (open) list.current?.children[active]?.scrollIntoView({ block: 'nearest' });
  }, [open, active]);

  const key = (e: KeyboardEvent) => {
    const n = options.length;
    if (!open) {
      if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) { e.preventDefault(); show(); }
      return;
    }
    const move: Record<string, number> = { ArrowDown: active + 1, ArrowUp: active - 1, Home: 0, End: n - 1 };
    if (e.key in move) {
      e.preventDefault();
      setActive(Math.min(n - 1, Math.max(0, move[e.key])));
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (options[active]) pick(options[active]);
    } else if (e.key === 'Tab') {
      setOpen(false);
    } else if (e.key.length === 1) {
      const ch = e.key.toLowerCase();
      for (let i = 1; i <= n; i++) {
        const j = (active + i) % n;
        if (options[j].label.toLowerCase().startsWith(ch)) { setActive(j); break; }
      }
    }
  };

  return (
    <div class={'select' + (cls ? ' ' + cls : '')} ref={ref}>
      <button type="button" class={'select-btn' + (open ? ' on' : '')} disabled={disabled} aria-haspopup="listbox"
        aria-expanded={open} aria-label={label} onClick={() => (open ? setOpen(false) : show())} onKeyDown={key}>
        <span class={current ? 'ellipsis' : 'ellipsis dim'}>{current?.label ?? placeholder}</span>
        <Icon name="down" size={14} />
      </button>
      {open && (
        <div class="menu" role="listbox" ref={list} aria-label={label}>
          {options.map((o, i) => (
            <div key={o.value} role="option" aria-selected={o.value === value}
              class={'menu-item' + (o.value === value ? ' on' : '') + (i === active ? ' active' : '')}
              onPointerMove={() => setActive(i)} onPointerDown={e => e.preventDefault()} onClick={() => pick(o)}>
              <span class="ellipsis">{o.label}</span>
              {o.hint && <span class="dim menu-note">{o.hint}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
