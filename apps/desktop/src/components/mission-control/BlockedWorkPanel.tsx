import { useQueries } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../../lib/api";
import { useProjects } from "../../lib/hooks";
import { allTasks } from "../../lib/projectHealth";
import { Panel } from "../Panel";

const CAP = 10;

/// Cross-project: fans out one getProject call per project (the same
/// ["project", id] query every other panel already warms) rather than
/// adding a new aggregate endpoint, then flattens+caps the blocked tasks
/// across all of them. Real task titles/reasons, not just a per-project
/// count, since troubleshooting needs to know *what* is stuck.
export function BlockedWorkPanel() {
  const { data: projects } = useProjects();
  const projectQueries = useQueries({
    queries: (projects ?? []).map((p) => ({
      queryKey: ["project", p.id],
      queryFn: () => api.getProject(p.id),
    })),
  });

  const blocked = projectQueries.flatMap((q) => {
    const project = q.data;
    if (!project) return [];
    return allTasks(project)
      .filter((t) => t.status === "blocked")
      .map((task) => ({ task, projectId: project.id, projectSlug: project.slug }));
  });
  const visible = blocked.slice(0, CAP);

  return (
    <Panel title="Blocked Work">
      {blocked.length === 0 ? (
        <div className="text-sm text-slate-500">Nothing blocked.</div>
      ) : (
        <div className="flex flex-col gap-2">
          <ul className="flex flex-col gap-2">
            {visible.map(({ task, projectId, projectSlug }) => (
              <li key={task.id}>
                <Link
                  to={`/projects/${projectId}?tab=tasks`}
                  className="block rounded-md border border-rose-900/40 bg-rose-950/20 p-2 hover:bg-rose-950/40"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm font-medium text-slate-200">{task.title}</span>
                    <span className="shrink-0 text-xs text-slate-500">{projectSlug}</span>
                  </div>
                  <div className="mt-1 truncate text-xs text-rose-400">
                    {task.blocked_reason ?? "No reason recorded."}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          {blocked.length > CAP && (
            <Link to="/projects" className="text-xs text-slate-500 hover:text-slate-300">
              view all {blocked.length} blocked tasks →
            </Link>
          )}
        </div>
      )}
    </Panel>
  );
}
