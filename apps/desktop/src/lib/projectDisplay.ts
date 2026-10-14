import type { ProjectStatus } from "./types";

/// Centralized so every surface (project list rows, project detail header)
/// colors a project's status the same way -- mirrors taskDisplay.ts's
/// pattern for tasks.
export const PROJECT_STATUS_TEXT_COLOR: Record<ProjectStatus, string> = {
  active: "text-emerald-400",
  paused: "text-amber-400",
  archived: "text-slate-500",
};
