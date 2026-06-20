import type { ProjectDetail, Task, TaskNode } from "./types";

export interface ProjectHealthSummary {
  openObjectives: number;
  blockedTasks: number;
  reviewQueue: number;
  decisionQueue: number;
}

function flatten(nodes: TaskNode[]): Task[] {
  return nodes.flatMap((n) => [n.task, ...flatten(n.children)]);
}

export function allTasks(project: ProjectDetail): Task[] {
  return flatten(project.tasks);
}

export function computeProjectHealth(project: ProjectDetail): ProjectHealthSummary {
  const tasks = allTasks(project);
  return {
    openObjectives: project.tasks.filter((n) => n.task.status !== "done" && n.task.status !== "cancelled").length,
    blockedTasks: tasks.filter((t) => t.status === "blocked").length,
    reviewQueue: tasks.filter((t) => t.status === "review").length,
    decisionQueue: project.decisions.filter((d) => d.status === "open").length,
  };
}
