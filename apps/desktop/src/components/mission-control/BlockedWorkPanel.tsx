import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { allTasks } from "../../lib/projectHealth";
import { Panel } from "../Panel";

export function BlockedWorkPanel({ projectId }: { projectId: string }) {
  const { data } = useQuery({
    queryKey: ["project", projectId],
    queryFn: () => api.getProject(projectId),
  });

  const blocked = data ? allTasks(data).filter((t) => t.status === "blocked") : [];

  return (
    <Panel title="Blocked Work">
      {blocked.length === 0 ? (
        <div className="text-sm text-slate-500">Nothing blocked.</div>
      ) : (
        <ul className="flex flex-col gap-2">
          {blocked.map((t) => (
            <li key={t.id} className="rounded-md border border-rose-900/40 bg-rose-950/20 p-2">
              <div className="text-sm font-medium text-slate-200">{t.title}</div>
              <div className="mt-1 text-xs text-rose-400">{t.blocked_reason ?? "No reason recorded."}</div>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
