import type { Task, TaskStatus } from "../../lib/types";

const STATUS_LABEL: Record<TaskStatus, string> = {
  draft: "Draft",
  ready: "Waiting",
  assigned: "Waiting",
  in_progress: "Running",
  review: "Reviewing",
  approved: "Reviewing",
  done: "Complete",
  blocked: "Blocked",
  cancelled: "Cancelled",
};

const STATUS_COLOR: Record<TaskStatus, string> = {
  draft: "text-slate-500",
  ready: "text-slate-400",
  assigned: "text-slate-400",
  in_progress: "text-indigo-300",
  review: "text-amber-300",
  approved: "text-amber-300",
  done: "text-emerald-400",
  blocked: "text-rose-400",
  cancelled: "text-slate-600",
};

const STATUS_DOT: Record<TaskStatus, string> = {
  draft: "bg-slate-500",
  ready: "bg-slate-400",
  assigned: "bg-slate-400",
  in_progress: "bg-indigo-400",
  review: "bg-amber-400",
  approved: "bg-amber-400",
  done: "bg-emerald-400",
  blocked: "bg-rose-400",
  cancelled: "bg-slate-600",
};

const STATUS_BORDER: Record<TaskStatus, string> = {
  draft: "border-slate-700",
  ready: "border-slate-700",
  assigned: "border-slate-700",
  in_progress: "border-indigo-800",
  review: "border-amber-800",
  approved: "border-amber-800",
  done: "border-emerald-800",
  blocked: "border-rose-800",
  cancelled: "border-slate-700",
};

export interface TaskStatusDisplay {
  label: string;
  color: string;
  dot: string;
  border: string;
}

/**
 * The scheduler-centric status word for one task -- "Improving" for a
 * retry already in flight, "Blocked by dependency" for a Ready leaf the
 * scheduler still won't release, otherwise the plain per-status label.
 * Shared by TaskTree, AgentTree, and the task graph so the same task
 * always reads the same way (label, text color, dot, pill border)
 * everywhere it's drawn.
 */
export function displayStatus(task: Task): TaskStatusDisplay {
  if (task.status === "in_progress" && task.attempt > 1) {
    return { label: "Improving", color: STATUS_COLOR.in_progress, dot: STATUS_DOT.in_progress, border: STATUS_BORDER.in_progress };
  }
  if (task.status === "ready" && !task.is_executable && task.dependencies.some((d) => d.status !== "done")) {
    return { label: "Blocked by dependency", color: "text-amber-400", dot: "bg-amber-400", border: "border-amber-800" };
  }
  return {
    label: STATUS_LABEL[task.status],
    color: STATUS_COLOR[task.status],
    dot: STATUS_DOT[task.status],
    border: STATUS_BORDER[task.status],
  };
}
