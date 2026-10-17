import { useEffect, useState } from 'preact/hooks';
import { getMe, onMe, onTeams, teams as loadTeams, type AuthState, type Team } from './api';

// State many views share: who is signed in, the teams, and the sidebar's
// scope.

/** Who is signed in, kept current. */
export function useMe() {
  const [me, setMe] = useState<AuthState | undefined>(getMe);
  useEffect(() => onMe(setMe), []);
  return me;
}

/** Every team the viewer may see, kept current. */
export function useTeams() {
  const [list, setList] = useState<Team[]>();
  useEffect(() => { loadTeams().then(setList); return onTeams(setList); }, []);
  return list;
}

// The scope of the sidebar and of new sessions: every session the viewer
// may see, the workspace's own, or one team's. Remembered per browser.
export type Scope = 'all' | 'workspace' | string;
const SCOPE_KEY = 'beethoven.scope';
let scope: Scope = (() => { try { return localStorage.getItem(SCOPE_KEY) || 'all'; } catch { return 'all'; } })();
const scopeWaiters = new Set<(s: Scope) => void>();
export function setScope(s: Scope) {
  scope = s;
  try { localStorage.setItem(SCOPE_KEY, s); } catch { /* not remembered */ }
  scopeWaiters.forEach(f => f(s));
}
export function useScope(): [Scope, (s: Scope) => void] {
  const [v, setV] = useState(scope);
  useEffect(() => { scopeWaiters.add(setV); return () => { scopeWaiters.delete(setV); }; }, []);
  return [v, setScope];
}
/** The team a scope names, if it names one. */
export const scopeTeam = (s: Scope) => (s === 'all' || s === 'workspace' ? '' : s);

