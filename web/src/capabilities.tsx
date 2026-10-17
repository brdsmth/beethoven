import { useEffect, useState } from 'preact/hooks';
import { get, type Capability } from './api';

// Capabilities: toolchains a sandbox installs when it boots (a mobile app
// stack, Go, Rust…), switched on globally, per environment, in a
// repository's .beethoven/capabilities, or during a session.

let catalog: Promise<Capability[]> | undefined;
/** The catalog, loaded once per page. */
export const capabilities = () => (catalog ??= get<Capability[]>('/api/capabilities').catch(() => { catalog = undefined; return []; }));

export function useCapabilities() {
  const [list, setList] = useState<Capability[]>();
  useEffect(() => { capabilities().then(setList); }, []);
  return list;
}

/** A checklist of capabilities. inherited are on already (globally); secrets
 * are the names a sandbox here receives, to point out missing ones. */
export function CapabilityPicker({ value, set, inherited = [], secrets }: {
  value: string[]; set: (v: string[]) => void; inherited?: string[]; secrets?: string[];
}) {
  const list = useCapabilities();
  if (!list) return <p class="hint">Loading…</p>;
  return (
    <div class="caps">
      {list.map(c => {
        const global = inherited.includes(c.id), on = global || value.includes(c.id);
        const missing = secrets ? c.secrets.filter(s => !secrets.includes(s.name)) : [];
        return (
          <label key={c.id} class="cap">
            <input type="checkbox" checked={on} disabled={global} title={global ? 'On in the global settings' : undefined}
              onChange={e => set(e.currentTarget.checked ? [...value, c.id] : value.filter(x => x !== c.id))} />
            <span class="cap-main">
              <span class="cap-name">{c.name}{global && <span class="badge plain">Global</span>}</span>
              <span class="hint">{c.description}.</span>
              {on && missing.map(s => (
                <span key={s.name} class={'hint' + (s.required ? ' err' : '')}>
                  {s.required ? 'Needs' : 'Uses'} the secret <code>{s.name}</code> ({s.why}), which isn't set here.
                </span>
              ))}
            </span>
          </label>
        );
      })}
    </div>
  );
}
