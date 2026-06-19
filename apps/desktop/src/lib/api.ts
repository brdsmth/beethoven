import { invoke } from "@tauri-apps/api/core";
import type {
  Agent,
  AgentInstanceRecord,
  ActiveExecution,
  DecisionRecord,
  DecisionSummary,
  DiffSummary,
  GoalHeader,
  InboxItem,
  InboxItemRecord,
  InitiativeSummary,
  ProjectDetail,
  ProjectRecord,
  ProjectSummary,
  RuntimeStatus,
  Task,
  TaskDetail,
  TaskRecord,
  TimelineEvent,
  TimelineFilters,
  Workspace,
  WorkflowMap,
} from "./types";

export const api = {
  getProjects: () => invoke<ProjectSummary[]>("get_projects"),
  getProject: (projectId: string) => invoke<ProjectDetail>("get_project", { projectId }),
  getAgents: (projectId: string) => invoke<Agent[]>("get_agents", { projectId }),
  getTasks: (projectId: string) => invoke<Task[]>("get_tasks", { projectId }),
  getInbox: (projectId: string) => invoke<InboxItem[]>("get_inbox", { projectId }),
  getTimeline: (filters: TimelineFilters = {}) =>
    invoke<TimelineEvent[]>("get_timeline", {
      projectId: filters.projectId,
      agentId: filters.agentId,
      eventType: filters.eventType,
      since: filters.since,
      until: filters.until,
    }),
  runtimeStatus: () => invoke<RuntimeStatus>("runtime_status"),
  getActiveExecution: () => invoke<ActiveExecution[]>("get_active_execution"),
  getGoalHeader: (projectId: string) => invoke<GoalHeader>("get_goal_header", { projectId }),
  getWorkflowMap: (initiativeId: string) => invoke<WorkflowMap>("get_workflow_map", { initiativeId }),
  getHomeDir: () => invoke<string>("get_home_dir"),
  getInitiatives: () => invoke<InitiativeSummary[]>("get_initiatives"),
  getDecisions: () => invoke<DecisionSummary[]>("get_decisions"),
  getWorkspaces: () => invoke<Workspace[]>("get_workspaces"),
  getRecentDiffs: (limit?: number) => invoke<DiffSummary[]>("get_recent_diffs", { limit }),
  getTaskDetail: (taskId: string) => invoke<TaskDetail>("get_task_detail", { taskId }),

  // --- Inbox actions -------------------------------------------------
  answerInboxItem: (inboxId: string, response: string) =>
    invoke<InboxItemRecord>("answer_inbox_item", { inboxId, response }),
  resolveDecision: (decisionId: string, response: string) =>
    invoke<DecisionRecord>("resolve_decision", { decisionId, response }),

  // --- Project actions -------------------------------------------------
  pauseProject: (projectId: string) => invoke<ProjectRecord>("pause_project", { projectId }),
  resumeProject: (projectId: string) => invoke<ProjectRecord>("resume_project", { projectId }),
  archiveProject: (projectId: string) => invoke<ProjectRecord>("archive_project", { projectId }),

  // --- Task actions -------------------------------------------------
  retryTask: (taskId: string) => invoke<TaskRecord>("retry_task", { taskId }),
  cancelTask: (taskId: string) => invoke<TaskRecord>("cancel_task", { taskId }),
  reassignTask: (taskId: string, newAgentId: string) =>
    invoke<TaskRecord>("reassign_task", { taskId, newAgentId }),

  // --- Review actions -------------------------------------------------
  approveReview: (taskId: string) => invoke<TaskRecord>("approve_review", { taskId }),
  requestReviewChanges: (taskId: string, reason: string) =>
    invoke<TaskRecord>("request_review_changes", { taskId, reason }),

  // --- Agent actions -------------------------------------------------
  pauseAgent: (agentId: string) => invoke<AgentInstanceRecord>("pause_agent", { agentId }),
  resumeAgent: (agentId: string) => invoke<AgentInstanceRecord>("resume_agent", { agentId }),

  // --- Runtime controls -------------------------------------------------
  runtimeStart: () => invoke<void>("runtime_start"),
  runtimeStop: () => invoke<void>("runtime_stop"),
  runtimeRestart: () => invoke<void>("runtime_restart"),
};
