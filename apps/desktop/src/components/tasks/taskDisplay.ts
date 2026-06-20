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

/**
 * The scheduler-centric status word for one task -- "Improving" for a
 * retry already in flight, "Blocked by dependency" for a Ready leaf the
 * scheduler still won't release, otherwise the plain per-status label.
 * Shared by TaskGraph and DependencyGraph so the same task always reads
 * the same way everywhere it's drawn.
 */
export function displayStatus(task: Task): { label: string; color: string } {
  if (task.status === "in_progress" && task.attempt > 1) {
    return { label: "Improving", color: STATUS_COLOR.in_progress };
  }
  if (task.status === "ready" && !task.is_executable && task.dependencies.some((d) => d.status !== "done")) {
    return { label: "Blocked by dependency", color: "text-amber-400" };
  }
  return { label: STATUS_LABEL[task.status], color: STATUS_COLOR[task.status] };
}
