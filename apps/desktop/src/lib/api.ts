import { invoke } from "@tauri-apps/api/core";
import type {
  Agent,
  AgentHealth,
  AgentInstanceRecord,
  AgentTemplate,
  ActiveExecution,
  DecisionRecord,
  DecisionSummary,
  DiffSummary,
  GoalHeader,
  InboxItem,
  InboxItemRecord,
  PlanningRun,
  ProjectDetail,
  ProjectRecord,
  ProjectSummary,
  PromptSummary,
  RuntimeStatus,
  Task,
  TaskDetail,
  TaskPlanOutcome,
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
  getWorkflowMap: (taskId: string) => invoke<WorkflowMap>("get_workflow_map", { taskId }),
  getHomeDir: () => invoke<string>("get_home_dir"),
  getDecisions: () => invoke<DecisionSummary[]>("get_decisions"),
  getPlanningHistory: (projectId: string) => invoke<PlanningRun[]>("get_planning_history", { projectId }),
  getWorkspaces: () => invoke<Workspace[]>("get_workspaces"),
  getRecentDiffs: (limit?: number) => invoke<DiffSummary[]>("get_recent_diffs", { limit }),
  getTaskDetail: (taskId: string) => invoke<TaskDetail>("get_task_detail", { taskId }),
  getAgentHealth: () => invoke<AgentHealth[]>("get_agent_health"),
  getRecentPrompts: (limit?: number) => invoke<PromptSummary[]>("get_recent_prompts", { limit }),
  getAgentTemplates: () => invoke<AgentTemplate[]>("get_agent_templates"),

  // --- Inbox actions -------------------------------------------------
  answerInboxItem: (inboxId: string, response: string) =>
    invoke<InboxItemRecord>("answer_inbox_item", { inboxId, response }),
  resolveDecision: (decisionId: string, response: string) =>
    invoke<DecisionRecord>("resolve_decision", { decisionId, response }),

  // --- Project actions -------------------------------------------------
  createProject: (name: string, repositoryPath: string, defaultBranch: string) =>
    invoke<ProjectRecord>("create_project", { name, repositoryPath, defaultBranch }),
  pauseProject: (projectId: string) => invoke<ProjectRecord>("pause_project", { projectId }),
  resumeProject: (projectId: string) => invoke<ProjectRecord>("resume_project", { projectId }),
  archiveProject: (projectId: string) => invoke<ProjectRecord>("archive_project", { projectId }),
  updateProjectRepositoryPath: (projectId: string, repositoryPath: string) =>
    invoke<ProjectRecord>("update_project_repository_path", { projectId, repositoryPath }),
  updateProjectDefaultBranch: (projectId: string, defaultBranch: string) =>
    invoke<ProjectRecord>("update_project_default_branch", { projectId, defaultBranch }),

  // --- Task actions -------------------------------------------------
  createTask: (
    projectId: string,
    parentTaskId: string | null,
    title: string,
    description: string,
    successCriteria: string,
    targetScore: number | null,
    draft: boolean,
  ) =>
    invoke<TaskRecord>("create_task", {
      projectId,
      parentTaskId,
      title,
      description,
      successCriteria,
      targetScore,
      draft,
    }),
  planTask: (taskId: string) => invoke<TaskPlanOutcome>("plan_task", { taskId }),
  approveDraftTask: (taskId: string) => invoke<TaskRecord>("approve_draft_task", { taskId }),
  rejectDraftTask: (taskId: string) => invoke<TaskRecord>("reject_draft_task", { taskId }),
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
  spawnWorkerAgent: (projectId: string, roleDescription: string, name: string) =>
    invoke<AgentInstanceRecord>("spawn_worker_agent", { projectId, roleDescription, name }),
  spawnReviewerAgent: (projectId: string, roleDescription: string, name: string) =>
    invoke<AgentInstanceRecord>("spawn_reviewer_agent", { projectId, roleDescription, name }),
  archiveAgent: (agentId: string) => invoke<AgentInstanceRecord>("archive_agent", { agentId }),

  // --- Runtime controls -------------------------------------------------
  runtimeStart: () => invoke<void>("runtime_start"),
  runtimeStop: () => invoke<void>("runtime_stop"),
  runtimeRestart: () => invoke<void>("runtime_restart"),
};
