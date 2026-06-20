import type { ProjectDetail, Task, TaskNode } from "./types";

export interface ProjectHealthSummary {
  openObjectives: number;
  readyTasks: number;
  runningTasks: number;
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
    readyTasks: tasks.filter((t) => t.status === "ready").length,
    runningTasks: tasks.filter((t) => t.status === "in_progress").length,
    blockedTasks: tasks.filter((t) => t.status === "blocked").length,
    reviewQueue: tasks.filter((t) => t.status === "review").length,
    decisionQueue: project.decisions.filter((d) => d.status === "open").length,
  };
}

/**
 * "What's actually holding this project up right now," in priority order:
 * a Blocked leaf (most recently blocked first), else the non-done leaf
 * with the most other tasks waiting on it (`dependents`), else a task on a
 * retry attempt (already known to need more than one pass). `null` when
 * nothing is currently a bottleneck.
 */
export function currentBottleneck(project: ProjectDetail): Task | null {
  const tasks = allTasks(project).filter((t) => t.status !== "done" && t.status !== "cancelled");
  const leaves = tasks.filter((t) => !tasks.some((other) => other.parent_task_id === t.id));

  const blocked = leaves.filter((t) => t.status === "blocked");
  if (blocked.length > 0) {
    return blocked.reduce((latest, t) => (t.updated_at > latest.updated_at ? t : latest));
  }

  const withDependents = leaves.filter((t) => t.dependents.length > 0);
  if (withDependents.length > 0) {
    return withDependents.reduce((most, t) => (t.dependents.length > most.dependents.length ? t : most));
  }

  const retrying = leaves.filter((t) => t.status === "in_progress" && t.attempt > 1);
  if (retrying.length > 0) {
    return retrying.reduce((most, t) => (t.attempt > most.attempt ? t : most));
  }

  return null;
}
