export type ProjectStatus = "active" | "paused" | "archived";
export type TaskStatus =
  | "draft"
  | "ready"
  | "assigned"
  | "in_progress"
  | "review"
  | "approved"
  | "done"
  | "blocked"
  | "cancelled";
export type AgentStatus =
  | "created"
  | "idle"
  | "assigned"
  | "executing"
  | "completed"
  | "paused"
  | "archived";
export type AgentRole = "lead" | "worker" | "reviewer";
export type InboxKind = "decision" | "approval" | "question" | "alert";
export type InboxStatus = "open" | "resolved";
export type DecisionStatus = "open" | "resolved";
export type RunStatus = "running" | "completed" | "failed";
export type ArtifactKind = "prompt" | "response" | "diff" | "files_modified";
export type ReviewStatus = "pending" | "approved" | "needs_changes";
export type AgentHealthStatus = "healthy" | "slow" | "stalled";

export interface TaskCounts {
  draft: number;
  ready: number;
  assigned: number;
  in_progress: number;
  review: number;
  approved: number;
  done: number;
  blocked: number;
  cancelled: number;
}

/// A compact reference to another task -- enough to link to and label it.
/// Backs `Task.dependencies`/`dependents`.
export interface TaskRef {
  id: string;
  title: string;
  status: TaskStatus;
}

export interface Task {
  id: string;
  project_id: string;
  parent_task_id: string | null;
  title: string;
  description: string;
  status: TaskStatus;
  assigned_agent_id: string | null;
  assigned_agent_name: string | null;
  attempt: number;
  branch: string | null;
  pr_url: string | null;
  blocked_reason: string | null;
  success_criteria: string;
  target_score: number;
  /// Tasks this one depends on -- it cannot become executable until every
  /// one of these is `done`.
  dependencies: TaskRef[];
  /// Tasks that depend on this one.
  dependents: TaskRef[];
  /// Computed fresh on every read: `Ready`, a leaf, its project `Active`,
  /// every dependency `done`, and no unresolved Decision against it
  /// directly. Never persisted -- recompute, don't trust a stale copy.
  is_executable: boolean;
  created_at: string;
  updated_at: string;
}

/// One task plus its children, recursively -- the single planning
/// primitive's tree, replacing the old Initiative(epics(tasks))/Goal(tasks)
/// nesting with one shape at every level.
export interface TaskNode {
  task: Task;
  children: TaskNode[];
}

export interface Decision {
  id: string;
  project_id: string;
  task_id: string | null;
  question: string;
  context: string;
  answer: string | null;
  status: DecisionStatus;
  created_at: string;
}

export interface ProjectSummary {
  id: string;
  name: string;
  slug: string;
  status: ProjectStatus;
  task_counts: TaskCounts;
  open_inbox_count: number;
  created_at: string;
  updated_at: string;
}

export interface ProjectDetail {
  id: string;
  name: string;
  slug: string;
  repository_path: string;
  default_branch: string;
  status: ProjectStatus;
  tasks: TaskNode[];
  decisions: Decision[];
  created_at: string;
  updated_at: string;
}

export interface Agent {
  id: string;
  project_id: string;
  template_id: string;
  role: AgentRole;
  role_description: string;
  parent_agent_id: string | null;
  name: string;
  status: AgentStatus;
  current_task_id: string | null;
  current_task_title: string | null;
  last_heartbeat: string | null;
  created_at: string;
}

export interface AgentHealth {
  agent_id: string;
  agent_name: string;
  role: AgentRole;
  role_description: string;
  project_id: string;
  project_slug: string;
  current_task_id: string | null;
  current_task_title: string | null;
  last_heartbeat: string | null;
  health: AgentHealthStatus;
}

export interface InboxItem {
  id: string;
  project_id: string;
  kind: InboxKind;
  status: InboxStatus;
  prompt: string;
  task_id: string | null;
  task_title: string | null;
  agent_id: string | null;
  agent_name: string | null;
  decision_id: string | null;
  decision_question: string | null;
  response: string | null;
  created_at: string;
  resolved_at: string | null;
}

export interface TimelineEvent {
  id: string;
  project_id: string | null;
  event_type: string;
  actor_id: string | null;
  payload: unknown;
  timestamp: string;
}

export interface RuntimeStatus {
  daemon_running: boolean;
  pid: number | null;
  stale_pidfile: boolean;
  last_started: string | null;
  last_stopped: string | null;
  active_agent_count: number;
  active_task_count: number;
  open_inbox_count: number;
  /// Non-Lead agents currently Assigned/Executing.
  worker_count: number;
  /// Tasks the scheduler would release right now across every Active
  /// project -- `is_executable`, not merely `ready`.
  queue_depth: number;
  ready_task_count: number;
  blocked_task_count: number;
  /// Tasks whose most recent Run failed -- distinct from blocked, which
  /// also covers a raised question or a low review score.
  failed_task_count: number;
}

export interface ActiveExecution {
  task_id: string;
  task_title: string;
  project_id: string;
  project_slug: string;
  agent_id: string;
  agent_name: string;
  agent_role: AgentRole;
  started_at: string;
}

/// The Mission Control header's progress banner -- the most recently
/// updated open root task (the old Initiative/Goal header, unified) and how
/// far its subtree has progressed.
export interface GoalHeader {
  project_id: string;
  project_name: string;
  active_task_id: string | null;
  active_label: string | null;
  percent_complete: number;
}

/// A root task's descendant subtree, bucketed by status -- the old
/// Initiative/Epic-specific stage map, generalized to any task tree.
export interface WorkflowMap {
  draft_count: number;
  ready_count: number;
  executing_count: number;
  review_count: number;
  done_count: number;
  blocked_count: number;
}

export interface TimelineFilters {
  projectId?: string;
  agentId?: string;
  eventType?: string;
  since?: string;
  until?: string;
}

// --- Cross-project / operator-mode read views (Phase 1.9b) ---------------

export interface DecisionSummary {
  id: string;
  project_id: string;
  project_slug: string;
  task_id: string | null;
  task_title: string | null;
  question: string;
  context: string;
  status: DecisionStatus;
  created_at: string;
}

export interface Workspace {
  task_id: string;
  task_title: string;
  project_id: string;
  project_slug: string;
  agent_id: string;
  agent_name: string;
  branch: string;
  git_worktree: string;
  tmux_session: string;
}

export interface DiffSummary {
  id: string;
  task_id: string;
  task_title: string;
  project_id: string;
  project_slug: string;
  content: string;
  created_at: string;
}

export interface Run {
  id: string;
  agent_id: string;
  attempt: number;
  provider: string;
  status: RunStatus;
  summary: string | null;
  prompt: string | null;
  response: string | null;
  started_at: string;
  completed_at: string | null;
}

export interface Artifact {
  id: string;
  kind: ArtifactKind;
  content: string;
  created_at: string;
}

/// One `Review` of a task's run -- the evaluation that drives the
/// score-vs-target_score retry decision (Beethoven's loop, with no separate
/// Loop entity).
export interface Review {
  id: string;
  status: ReviewStatus;
  score: number;
  summary: string;
  recommendations: string[];
  created_at: string;
}

export interface TaskDetail {
  task: Task;
  runs: Run[];
  reviews: Review[];
  artifacts: Artifact[];
  timeline: TimelineEvent[];
}

export interface PromptSummary {
  id: string;
  run_id: string;
  task_id: string;
  task_title: string;
  project_id: string;
  project_slug: string;
  content: string;
  created_at: string;
}

export interface RuntimeProfile {
  id: string;
  runtime_id: string;
  model: string;
}

export interface AgentTemplate {
  id: string;
  role: AgentRole;
  label: string;
  prompt: string;
  permissions: string[];
  tools: string[];
  runtime_profile: RuntimeProfile | null;
  created_at: string;
}

/// What `plan_task` returns: the children it produced (real work to do) and
/// any decisions auto-escalated to the inbox.
export interface TaskPlanOutcome {
  tasks: Task[];
  decisions: Decision[];
}

// --- Raw mutation return shapes -------------------------------------------
//
// Action commands (`pause_project`, `retry_task`, etc.) hand back the
// underlying beethoven-core domain struct as-is, not the enriched `*Dto`
// shape the list/detail read commands build -- so these are deliberately
// narrower than ProjectSummary/Task/Agent/InboxItem above. Callers
// invalidate the relevant query rather than reading off these directly.

export interface ProjectRecord {
  id: string;
  name: string;
  slug: string;
  repository_path: string;
  default_branch: string;
  status: ProjectStatus;
  created_at: string;
  updated_at: string;
}

export interface TaskRecord {
  id: string;
  project_id: string;
  parent_task_id: string | null;
  title: string;
  description: string;
  status: TaskStatus;
  assigned_agent_id: string | null;
  branch: string | null;
  pr_url: string | null;
  blocked_reason: string | null;
  attempt: number;
  success_criteria: string;
  target_score: number;
  created_at: string;
  updated_at: string;
}

export interface AgentInstanceRecord {
  id: string;
  project_id: string;
  template_id: string;
  role: AgentRole;
  role_description: string;
  parent_agent_id: string | null;
  name: string;
  status: AgentStatus;
  current_task_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface InboxItemRecord {
  id: string;
  project_id: string;
  kind: InboxKind;
  task_id: string | null;
  agent_id: string | null;
  decision_id: string | null;
  prompt: string;
  status: InboxStatus;
  response: string | null;
  created_at: string;
  resolved_at: string | null;
}

export interface DecisionRecord {
  id: string;
  project_id: string;
  task_id: string | null;
  question: string;
  context: string;
  answer: string | null;
  status: DecisionStatus;
  created_at: string;
}
