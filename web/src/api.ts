export type Author = { name: string; email: string };
export type RepoRef = { repo: string; base_branch: string; pr_url?: string };
export type Session = {
  id: string; title: string; repos: RepoRef[]; environment_id?: string; branch: string; model: string; effort: string;
  parent_id?: string; depth: number; source: string; key?: string; automation_id?: string; author: Author;
  status: 'idle' | 'running' | 'done' | 'failed' | 'stopped';
  sandbox: 'none' | 'starting' | 'ready' | 'stopped' | 'failed';
  cost_usd: number; archived: boolean; created_at: string; updated_at: string;
};
export type Prompt = {
  id: string; session_id: string; content: string; model?: string; effort?: string; author: Author; source: string;
  automation_id?: string; status: 'queued' | 'running' | 'done' | 'failed' | 'cancelled';
  error?: string; result?: string; cost_usd: number; created_at: string; started_at?: string; finished_at?: string;
};
export type Ev = { session: string; seq?: number; type: string; prompt?: string; data?: any; at: string };
/** What a session works on: an environment, repositories, or neither (scratch). */
export type Target = { environment_id: string; repos: RepoRef[] };
export type Spec = Target & {
  name: string; prompt: string; model: string; effort: string; schedule: string; reuse: boolean; enabled: boolean;
};
export type Automation = Spec & { id: string; failures: number; next_run_at?: string; created_at: string };
export type Info = {
  version: string; author: Author; github: boolean; anthropic: boolean; model: string; effort: string; efforts: string[];
};
export type Overrides = { model?: string; effort?: string; instructions?: string; image?: string; cpus?: number; memory_mb?: number };
export type Settings = Overrides & {
  idle_minutes?: number; turn_minutes?: number; draft_prs?: boolean; branch_prefix?: string;
  author_name?: string; author_email?: string;
};
export type SettingsInfo = {
  settings: Settings; efforts: string[];
  defaults: { image: string; idle_minutes: number; turn_minutes: number; branch_prefix: string; author_name: string; author_email: string };
};
export type Credentials = {
  stored: Record<string, boolean>;
  anthropic: { source: string };
  github: { source: string; login?: string; error?: string };
};
export type Environment = {
  id: string; repos: RepoRef[]; name: string; description: string; setup_script: string; start_script: string;
  inherit_secrets: boolean; overrides: Overrides; created_at: string; updated_at: string;
};
export type Secret = { scope: string; name: string; updated_at: string };
export type Effective = { layers: string[]; secrets: { name: string; scope: string; overrides?: string[] }[] };
export type MCPServer = {
  id: string; name: string; scope: string; type: 'http' | 'stdio'; url?: string; command?: string[];
  secret_keys: string[]; enabled: boolean; created_at: string; updated_at: string;
};
/** A pull request a session opened, as last seen on GitHub. */
export type PullRequest = {
  repo: string; number: number; session_id: string; url: string; state: 'open' | 'merged' | 'closed'; draft: boolean;
  opened_at: string; merged_at?: string; closed_at?: string; synced_at: string;
};
export type Count = { label: string; n: number };
export type InsightDay = {
  date: string; sessions: number; first_try: number; more_cycles: number; unsuccessful: number; merged: number; spend: number;
};
export type InsightGroup = {
  name: string; sessions: number; settled: number; first_try_rate: number; cycles: number; merged: number; spend: number;
};
/** GET /api/insights: how the sessions of a window went. */
export type Insights = {
  from: string; to: string;
  sessions: number; settled: number; cycles: number; cycle_counts: Count[]; first_try_rate: number; success_rate: number;
  prompts: number; failed: number; stopped: number; failures: Count[]; agent_seconds: number;
  pull_requests: {
    opened: number; merged: number; closed: number; open: number; acceptance_rate: number | null;
    median_to_open_secs: number; median_to_merge_secs: number; cost_per_merged: number; sessions_with_merged: number;
  };
  automations: { runs: number; succeeded: number; failed: number; spend: number };
  spend: number;
  days: InsightDay[]; by_source: InsightGroup[]; by_person: InsightGroup[]; by_repo: InsightGroup[];
};
export type GitHubRepo = { repo: string; description?: string; private: boolean; default_branch: string };
export type SlackStatus = {
  /** The Slack app the server acts as: preconfigured (environment) or created here (settings). */
  app: { app_id: string; source: 'environment' | 'settings'; settings_url: string } | null;
  installations: { team_id: string; team_name: string; installer: string; base_url: string; created_at: string }[];
  /** What a channel works on when a request names nothing. */
  channels: { team_id: string; channel: string; name: string; environment_id?: string; repos: RepoRef[] }[];
};
export type GitHubStatus = {
  /** The GitHub App the server acts as: preconfigured (environment) or registered here (settings). */
  app: { name: string; slug: string; owner: string; url: string; install_url: string; source: 'environment' | 'settings' } | null;
  /** Who connected it; the server acts for them. */
  account: { id: number; login: string; name: string } | null;
  installations: { id: number; account: string; type: string; selection: 'all' | 'selected'; url: string }[];
  /** Where a personal access token comes from; used until someone connects. */
  token: string;
  error?: string;
};

/** A person who signs in to the web UI. */
export type Account = { id: string; email: string; name: string; owner: boolean };
/** Whether this server asks people to sign in, and who is signed in. */
export type AuthState = {
  required: boolean;
  /** No account exists yet: the first one created owns the server. */
  setup: boolean;
  account: Account | null;
};

const authWaiters = new Set<() => void>();
export function onAuthRequired(fn: () => void) {
  authWaiters.add(fn);
  return () => { authWaiters.delete(fn); };
}

export async function api<T = void>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (method !== 'GET') headers['Content-Type'] = 'application/json';
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (res.status === 401) authWaiters.forEach(f => f());
  if (!res.ok) {
    const msg = await res.json().then(j => j?.error, () => '');
    throw Object.assign(new Error(msg || `${res.status} ${res.statusText}`), { status: res.status });
  }
  return res.status === 204 ? (undefined as T) : res.json();
}
export const get = <T>(path: string) => api<T>('GET', path);

/** Follows a server-sent event stream. The browser resumes dropped streams on
 * its own (with Last-Event-ID); if the server refuses one outright, it is
 * reopened from path() after a pause, and a probe surfaces a 401 (signed out). */
export function subscribe(path: () => string, onEvent: (e: Ev) => void, onOpen?: () => void) {
  let es: EventSource | undefined, timer = 0, done = false;
  const open = () => {
    const p = path();
    es = new EventSource(p);
    es.onopen = () => onOpen?.();
    es.onmessage = m => { try { onEvent(JSON.parse(m.data)); } catch (err) { console.error(err); } };
    es.onerror = () => {
      if (done || es?.readyState !== EventSource.CLOSED) return;
      get('/api/info').catch(() => {});
      timer = window.setTimeout(open, 3000);
    };
  };
  open();
  return () => { done = true; clearTimeout(timer); es?.close(); };
}

// One shared stream of every session's state changes.
type GlobalSub = { ev: (e: Ev) => void; open?: () => void };
const globalSubs = new Set<GlobalSub>();
let globalStream: (() => void) | undefined;
export function onGlobal(ev: (e: Ev) => void, open?: () => void) {
  const sub = { ev, open };
  globalSubs.add(sub);
  globalStream ??= subscribe(() => '/api/events', e => globalSubs.forEach(s => s.ev(e)),
    () => globalSubs.forEach(s => s.open?.()));
  return () => { globalSubs.delete(sub); };
}

// GitHub lookups for pickers, cached for the page's lifetime; failures
// resolve empty so inputs fall back to free text.
let repoList: Promise<GitHubRepo[]> | undefined;
let branchLists: Record<string, Promise<string[]>> = {};
export const resetGitHub = () => { repoList = undefined; branchLists = {}; };
export const githubRepos = () => (repoList ??= get<GitHubRepo[]>('/api/repos').catch(() => []));
export const isRepo = (s: string) => /^[\w.-]+\/[\w.-]+$/.test(s);
export const githubBranches = (repo: string) => (branchLists[repo] ??= get<string[]>(`/api/repos/${repo}/branches`)
  .catch(() => { delete branchLists[repo]; return []; }));

/** Query string for a secret scope. */
export const scopeQuery = (scope: string) => 'scope=' + encodeURIComponent(scope);
