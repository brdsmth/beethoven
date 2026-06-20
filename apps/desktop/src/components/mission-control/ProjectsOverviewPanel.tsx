import { useProjects } from "../../lib/hooks";
import { Panel } from "../Panel";
import type { ProjectSummary } from "../../lib/types";

const STATUS_COLOR: Record<ProjectSummary["status"], string> = {
  active: "bg-emerald-400",
  paused: "bg-amber-400",
  archived: "bg-slate-600",
};

export function ProjectsOverviewPanel({
  selectedProjectId,
  onSelect,
}: {
  selectedProjectId: string;
  onSelect: (id: string) => void;
}) {
  const { data } = useProjects();
  const projects = data ?? [];
  const active = projects.filter((p) => p.status === "active").length;

  return (
    <Panel title="Projects">
      {projects.length === 0 ? (
        <div className="text-sm text-slate-500">No projects yet.</div>
      ) : (
        <div className="flex flex-col gap-2">
          <div className="text-xs text-slate-500">
            {active} active · {projects.length} total
          </div>
          <ul className="flex flex-col gap-1">
            {projects.map((p) => (
              <li key={p.id}>
                <button
                  onClick={() => onSelect(p.id)}
                  className={`flex w-full items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors ${
                    p.id === selectedProjectId ? "bg-indigo-500/20 text-indigo-300" : "text-slate-300 hover:bg-slate-800/60"
                  }`}
                >
                  <span className="flex items-center gap-2">
                    <span className={`h-1.5 w-1.5 rounded-full ${STATUS_COLOR[p.status]}`} />
                    {p.name}
                  </span>
                  {p.open_inbox_count > 0 && (
                    <span className="text-xs text-amber-400">{p.open_inbox_count}</span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Panel>
  );
}
