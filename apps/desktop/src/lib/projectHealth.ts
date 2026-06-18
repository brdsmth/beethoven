import type { ProjectDetail, Task } from "./types";

export interface ProjectHealthSummary {
  activeInitiatives: number;
  blockedTasks: number;
  reviewQueue: number;
  decisionQueue: number;
}

function allTasks(project: ProjectDetail): Task[] {
  const goalTasks = project.goals.flatMap((g) => g.tasks);
  const epicTasks = project.initiatives.flatMap((i) => i.epics.flatMap((e) => e.tasks));
  return [...goalTasks, ...epicTasks];
}

export function computeProjectHealth(project: ProjectDetail): ProjectHealthSummary {
  const tasks = allTasks(project);
  return {
    activeInitiatives: project.initiatives.filter((i) => i.status !== "completed").length,
    blockedTasks: tasks.filter((t) => t.status === "blocked").length,
    reviewQueue: tasks.filter((t) => t.status === "review").length,
    decisionQueue: project.initiatives
      .flatMap((i) => i.decisions)
      .filter((d) => d.status === "open").length,
  };
}
