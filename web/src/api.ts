export type Author = { name: string; email: string };
export type RepoRef = { repo: string; base_branch: string; pr_url?: string };
export type Session = {
  id: string; title: string; repos: RepoRef[]; environment_id?: string; branch: string; model: string; effort: string;
  parent_id?: string; depth: number; source: string; key?: string; automation_id?: string; author: Author;
  /** The person it works for; their GitHub opens its pull requests. */
  account_id?: string;
  /** The team that owns it; none = the workspace. */
  team_id?: string;
  visibility: Visibility;
  /** People it's shared with by name, who may read it. */
  shared_with?: string[];
  status: 'idle' | 'running' | 'done' | 'failed' | 'stopped';
  sandbox: 'none' | 'starting' | 'ready' | 'stopped' | 'failed';
  cost_usd: number; archived: boolean; created_at: string; updated_at: string;
};
export type Prompt = {
  id: string; session_id: string; content: string; model?: string; effort?: string; author: Author; source: string;
  automation_id?: string; status: 'queued' | 'running' | 'done' | 'failed' | 'cancelled';
  error?: string; result?: string; cost_usd: number; created_at: string; started_at?: string; finished_at?: string;
  attachments?: Attachment[];
  /** Exchanges between agents that led to it; absent for people's. */
  hop?: number;
};
/** A file attached to a prompt. */
export type Attachment = { id: string; name: string; content_type: string; size: number };
export const attachmentURL = (session: string, a: Attachment) => `/api/sessions/${session}/attachments/${a.id}`;
export type Ev = { session: string; seq?: number; type: string; prompt?: string; data?: any; at: string };
/** What a session works on: an environment, repositories, or neither (scratch). */
export type Target = { environment_id: string; repos: RepoRef[] };
/** Which git host events fire an automation: one kind, with every condition true. */
export type Condition = { type: string; values: string[]; negate?: boolean; regex?: boolean };
export type Trigger = { event: string; conditions: Condition[] };
export type EventKind = { id: string; label: string; conditions: string[] };
export type Spec = Target & {
  name: string; prompt: string; model: string; effort: string; schedule: string; reuse: boolean; enabled: boolean;
  team_id?: string; trigger?: Trigger;
};
export type Automation = Spec & {
  id: string; failures: number; next_run_at?: string; created_at: string; account_id?: string;
  trigger_event: string; trigger_conditions: Condition[];
};
export type Info = {
  version: string; author: Author; github: boolean; anthropic: boolean; model: string; effort: string; efforts: string[];
};
export type Overrides = {
  model?: string; effort?: string; instructions?: string; image?: string; cpus?: number; memory_mb?: number; capabilities?: string[];
};
/** A toolchain sandboxes can be given: installed when one boots, with a skill that tells the agent how to use it. */
export type Capability = {
  id: string; name: string; description: string; install: string; prepare?: string;
  secrets: { name: string; required: boolean; why: string }[]; env?: Record<string, string>; cpus?: number; memory_mb?: number;
};
/** What adding a capability to a session did: where it is kept for later. */
export type AddedCapability = { id: string; name: string; kept: 'environment' | 'repository' | 'session'; where?: string; warnings?: string[] };
export type Settings = Overrides & {
  idle_minutes?: number; turn_minutes?: number; draft_prs?: boolean; branch_prefix?: string;
  author_name?: string; author_email?: string; slack_members_only?: boolean;
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
  id: string; team_id?: string; repos: RepoRef[]; name: string; description: string; setup_script: string; start_script: string;
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
export type Usage = { input_tokens: number; output_tokens: number; cache_read_tokens: number; cache_write_tokens: number };
export type InsightScope = 'people' | 'automations' | 'agents' | 'all';
/** A window's headline measures. */
export type InsightTotals = {
  sessions: number; settled: number; cycles: number; first_try_rate: number; success_rate: number; people: number;
  prompts: number; agent_seconds: number; spend: number; per_session: number; automation_spend: number;
  usage: Usage; cache_hit_rate: number | null;
  opened: number; merged: number; closed: number; open: number; acceptance_rate: number | null;
  median_to_open_secs: number; median_to_merge_secs: number; p90_to_merge_secs: number; cost_per_merged: number;
};
export type InsightDay = {
  date: string; sessions: number; settled: number; cycles: number; first_try: number; more_cycles: number; unsuccessful: number;
  people: number; origins: number[]; opened: number; merged: number; spend: number; spend_by: number[];
};
export type InsightGroup = {
  name: string; sessions: number; settled: number; first_try_rate: number; cycles: number; prompts: number; agent_seconds: number;
  merged: number; spend: number; cost_per_merged: number; usage: Usage; prev_spend?: number;
};
/** A session in scope with the work it accounts for. */
export type InsightRun = {
  id: string; title: string; person: string; created_at: string; children: number; cycles: number; agent_seconds: number;
  status: string; pr: '' | 'merged' | 'open' | 'closed'; spend: number;
};
export type PRGroup = {
  name: string; opened: number; merged: number; closed: number; acceptance_rate: number | null; median_to_merge_secs: number; cost_per_merged: number;
};
export type InsightPerson = InsightGroup & {
  account_id?: string; last_active: string; daily: number[]; repos: Count[]; recent: InsightRun[];
};
/** GET /api/insights: how the sessions of a window went, and the window before. */
export type Insights = {
  from: string; to: string; scope: InsightScope; repos: string[];
  totals: InsightTotals; previous: InsightTotals;
  cycle_counts: Count[]; days: InsightDay[]; sources: Count[];
  ended: { done: number; failed: number; stopped: number; active: number; archived: number };
  hours: number[][]; children_per_root: number;
  by_repo: InsightGroup[]; by_project: InsightGroup[]; by_team: InsightGroup[]; by_model: InsightGroup[];
  by_model_effort: InsightGroup[]; by_source: InsightGroup[]; runs: InsightRun[];
  pull_requests: {
    draft: number; to_merge: Count[]; by_source: PRGroup[]; by_repo: PRGroup[]; open_now: number; oldest_open_secs: number;
    waiting: { repo: string; number: number; url: string; draft: boolean; opened_at: string; session_id: string; title: string; person: string }[];
  };
  people: InsightPerson[];
  weeks: { start: string; people: number }[];
  funnel: { invited: number; joined: number; started: number; merged: number; median_to_start_secs: number };
};
export type Spread = { n: number; median: number; p90: number; prev_median: number };
/** GET /api/insights/reliability, for admins: how reliably a window's work ran. */
export type Reliability = {
  from: string; to: string; scope: InsightScope;
  prompts: number; failed: number; stopped: number; failure_rate: number | null; prev_failure_rate: number | null;
  duration: Spread; queue_wait: Spread; sandbox_start: Spread;
  tool_calls: number; tool_errors: number; tools: { name: string; calls: number; errors: number }[];
  days: { date: string; failed: number; stopped: number }[];
  failures: { label: string; n: number; daily: number[]; last_at: string; session_id: string }[];
  automations: {
    id: string; name: string; trigger: string; enabled: boolean; failures: number; runs: number; succeeded: number; failed: number;
    merged: number; spend: number; last_status?: string; last_at?: string;
  }[];
};
/** A thread elsewhere a session takes part in: a pull request it opened, an issue it commented on. */
export type ThreadLink = {
  subject: string; session_id: string; role: 'owner' | 'follower'; reason: string; muted: boolean; created_at: string; closed_at?: string;
};
/** Something that happened on one of a session's threads, and what became of it. */
export type InboxItem = {
  id: number; subject: string; kind: string; actor: string; received_at: string; delivered_at?: string; prompt_id?: string; outcome?: string;
  data: { url?: string; comment_url?: string; number?: number; repo?: string };
};
export type GitHubRepo = { repo: string; description?: string; private: boolean; default_branch: string };
export type SlackStatus = {
  /** The Slack app the server acts as: preconfigured (environment) or created here (settings). */
  app: { app_id: string; source: 'environment' | 'settings'; settings_url: string } | null;
  installations: { team_id: string; team_name: string; installer: string; base_url: string; created_at: string }[];
  /** What a channel works on when a request names nothing. */
  channels: { team_id: string; channel: string; name: string; environment_id?: string; repos: RepoRef[]; owner_team_id?: string }[];
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

/** Who may read a session: everyone, its team, or only whoever started it. */
export type Visibility = 'workspace' | 'team' | 'private';
export type Role = 'owner' | 'admin' | 'member';
/** A person who signs in to the web UI. */
export type Account = { id: string; email: string; name: string; role: Role; owner: boolean; disabled?: boolean; created_at: string };
/** Someone as others see them: emails only for admins. */
export type Person = Account & { github?: string };
/** Whether this server asks people to sign in, and who is signed in. */
export type AuthState = {
  required: boolean;
  /** No account exists yet: the first one created owns the server. */
  setup: boolean;
  account: Account | null;
  /** The viewer's teams: id → lead or member. */
  teams: Record<string, 'lead' | 'member'>;
  default_team_id: string;
  /** The viewer manages the workspace. */
  admin: boolean;
  /** The server has accounts, so people and teams apply. */
  people: boolean;
  /** Where they stand in getting started, while they are. */
  onboarding?: Onboarding;
};
/** Someone's getting started: who invited them, to which team, and a first session they suggested. */
export type Onboarding = {
  team_id?: string; invited_by?: string; invited_by_name?: string; prompt?: string; environment_id?: string;
  /** The welcome tour is still to show. */
  tour?: boolean;
  /** They started a session. */
  started?: boolean;
  /** When it began: they joined, or joined the team. */
  since?: string;
};
/** What an invitation's link shows: in full while it's open, else what became of it. */
export type InviteInfo = {
  state: 'open' | 'used' | 'expired'; from?: string; created_at: string; expires_at: string;
  role?: Role; email?: string; note?: string; github?: boolean;
  team?: { name: string; description?: string; color: string; members: number; projects: string[] };
  /** Who is signed in already: the invitation adds them to its team. */
  signed_in?: { name: string; email: string };
};
export type Team = {
  id: string; slug: string; name: string; description: string; color: string;
  join_policy: 'open' | 'invite'; default_visibility: 'team' | 'workspace'; default_environment_id: string;
  archived_at?: string; members: number; created_at: string; updated_at: string;
  /** One person's own team: only they see it, and it's where their work goes unless they choose another. */
  personal?: boolean;
  /** The viewer's role in it, if any, and whether they may change it. */
  role?: 'lead' | 'member'; manages: boolean;
};
export type TeamMember = Account & { team_role: 'lead' | 'member'; joined_at: string };
export type Invite = {
  id: string; role: Role; email?: string; team_id?: string; team?: string; note?: string; first_prompt?: string; environment_id?: string;
  created_by: string; created_at: string; expires_at: string;
};
export type APIToken = { id: string; name: string; hint: string; created_at: string; last_used_at?: string };
export type AuditEvent = {
  id: number; at: string; actor_id?: string; actor: string; team_id?: string; action: string; target?: string; detail: Record<string, unknown>;
};

// Who is signed in, for any component (set by the root once known).
let me: AuthState | undefined;
const meWaiters = new Set<(a: AuthState) => void>();
export const setMe = (a: AuthState) => { me = a; meWaiters.forEach(f => f(a)); };
export const getMe = () => me;
export function onMe(fn: (a: AuthState) => void) {
  meWaiters.add(fn);
  return () => { meWaiters.delete(fn); };
}

// Every team, cached for the page's lifetime; reloadTeams refreshes them.
let teamList: Promise<Team[]> | undefined;
const teamWaiters = new Set<(t: Team[]) => void>();
export const teams = () => (teamList ??= get<Team[]>('/api/teams').catch(() => []));
export function reloadTeams() {
  teamList = undefined;
  teams().then(t => teamWaiters.forEach(f => f(t)));
  get<AuthState>('/api/auth').then(setMe, () => {});
}
export function onTeams(fn: (t: Team[]) => void) {
  teamWaiters.add(fn);
  return () => { teamWaiters.delete(fn); };
}

const authWaiters = new Set<() => void>();
export function onAuthRequired(fn: () => void) {
  authWaiters.add(fn);
  return () => { authWaiters.delete(fn); };
}

/** Sends JSON, or a form made by withFiles. */
export async function api<T = void>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const form = body instanceof FormData;
  if (method !== 'GET' && !form) headers['Content-Type'] = 'application/json';
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : form ? body : JSON.stringify(body) });
  if (res.status === 401) authWaiters.forEach(f => f());
  if (!res.ok) {
    const msg = await res.json().then(j => j?.error, () => '');
    throw Object.assign(new Error(msg || `${res.status} ${res.statusText}`), { status: res.status });
  }
  return res.status === 204 ? (undefined as T) : res.json();
}
export const get = <T>(path: string) => api<T>('GET', path);

/** A request body with files attached: the JSON in a form's "request"
 * field, each file in a "file" field. Without files, the body itself. */
export function withFiles(body: unknown, files: File[]) {
  if (!files.length) return body;
  const form = new FormData();
  form.append('request', JSON.stringify(body));
  for (const f of files) form.append('file', f, f.name);
  return form;
}

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
