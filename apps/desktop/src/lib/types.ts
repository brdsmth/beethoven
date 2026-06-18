export type ProjectStatus = "active" | "archived";
export type GoalStatus = "open" | "planned" | "completed";
export type TaskStatus =
  | "draft"
  | "ready"
  | "assigned"
  | "in_progress"
  | "review"
  | "approved"
  | "done"
  | "blocked";
export type AgentStatus =
  | "created"
  | "idle"
  | "assigned"
  | "planning"
  | "executing"
  | "review"
  | "completed"
  | "blocked"
  | "waiting"
  | "escalated";
export type AgentKind =
  | "project_manager"
  | "backend"
  | "frontend"
  | "qa"
  | "research"
  | "dev_ops";
export type InboxKind = "decision" | "approval" | "question" | "alert";
export type InboxStatus = "open" | "resolved";
export type InitiativeStatus = "discovery" | "planned" | "completed";
export type DecisionStatus = "open" | "resolved";

export interface TaskCounts {
  draft: number;
  ready: number;
  assigned: number;
  in_progress: number;
  review: number;
  approved: number;
  done: number;
  blocked: number;
}

export interface Task {
  id: string;
  project_id: string;
  goal_id: string | null;
  epic_id: string | null;
  title: string;
  description: string;
  status: TaskStatus;
  assigned_agent_id: string | null;
  assigned_agent_name: string | null;
  attempt: number;
  branch: string | null;
  pr_url: string | null;
  created_at: string;
  updated_at: string;
}

export interface Requirement {
  id: string;
  description: string;
}

export interface Epic {
  id: string;
  title: string;
  tasks: Task[];
}

export interface Decision {
  id: string;
  title: string;
  context: string;
  status: DecisionStatus;
}

export interface Initiative {
  id: string;
  title: string;
  description: string;
  status: InitiativeStatus;
  requirements: Requirement[];
  epics: Epic[];
  decisions: Decision[];
  created_at: string;
  updated_at: string;
}

export interface Goal {
  id: string;
  title: string;
  description: string;
  status: GoalStatus;
  tasks: Task[];
  created_at: string;
  updated_at: string;
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
  status: ProjectStatus;
  initiatives: Initiative[];
  goals: Goal[];
  created_at: string;
  updated_at: string;
}

export interface Agent {
  id: string;
  project_id: string;
  kind: AgentKind;
  name: string;
  status: AgentStatus;
  current_task_id: string | null;
  current_task_title: string | null;
  last_heartbeat: string | null;
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
  decision_title: string | null;
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
}

export interface ActiveExecution {
  task_id: string;
  task_title: string;
  project_id: string;
  project_slug: string;
  agent_id: string;
  agent_name: string;
  agent_kind: AgentKind;
  started_at: string;
}

export type GoalHeaderSource = "initiative" | "goal" | "none";

export interface GoalHeader {
  project_id: string;
  project_name: string;
  source: GoalHeaderSource;
  active_label: string | null;
  active_initiative_id: string | null;
  percent_complete: number;
}

export interface WorkflowMap {
  initiative_count: number;
  epic_count: number;
  task_count: number;
  execution_count: number;
  review_count: number;
  complete_count: number;
}

export interface TimelineFilters {
  projectId?: string;
  agentId?: string;
  eventType?: string;
  since?: string;
  until?: string;
}
