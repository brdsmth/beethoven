import { invoke } from "@tauri-apps/api/core";
import type {
  Agent,
  ActiveExecution,
  GoalHeader,
  InboxItem,
  ProjectDetail,
  ProjectSummary,
  RuntimeStatus,
  Task,
  TimelineEvent,
  TimelineFilters,
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
};
