import { Link } from "react-router-dom";
import type { ProjectSummary } from "../../lib/types";

export function ProjectList({ projects }: { projects: ProjectSummary[] }) {
  if (projects.length === 0) {
    return <div className="text-sm text-slate-500">No projects yet.</div>;
  }

  return (
    <ul className="flex flex-col gap-2">
      {projects.map((p) => (
        <li key={p.id}>
          <Link
            to={`/projects/${p.id}`}
            className="flex items-center justify-between rounded-lg border border-slate-800 bg-slate-900/40 p-4 hover:border-indigo-600"
          >
            <div>
              <div className="font-medium text-slate-100">{p.name}</div>
              <div className="text-xs text-slate-500">{p.slug}</div>
            </div>
            <div className="flex items-center gap-4 text-sm text-slate-400">
              <span>{p.task_counts.in_progress} in progress</span>
              <span>{p.open_inbox_count} inbox</span>
              <span className={p.status === "active" ? "text-emerald-400" : "text-slate-500"}>{p.status}</span>
            </div>
          </Link>
        </li>
      ))}
    </ul>
  );
}
