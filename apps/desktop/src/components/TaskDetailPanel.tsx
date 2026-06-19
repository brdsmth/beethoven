import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { formatRelativeTime, humanizeEventType } from "../lib/format";
import { Panel } from "./Panel";

/**
 * Shared read-only view over one task's full execution history -- backs
 * both the "View Diff" and "View Logs" actions, since both are just
 * different slices of the same already-stored model runs/artifacts/timeline.
 */
export function TaskDetailPanel({ taskId }: { taskId: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ["taskDetail", taskId],
    queryFn: () => api.getTaskDetail(taskId),
  });

  if (isLoading || !data) {
    return <div className="text-sm text-slate-500">Loading…</div>;
  }

  const { task, model_runs, artifacts, timeline } = data;
  const diffs = artifacts.filter((a) => a.kind === "diff");
  const filesModified = artifacts.filter((a) => a.kind === "files_modified");

  return (
    <div className="flex flex-col gap-4">
      <div>
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold text-slate-100">{task.title}</h2>
          <span className="text-xs uppercase text-slate-500">{task.status}</span>
        </div>
        {task.description && <p className="mt-1 text-sm text-slate-400">{task.description}</p>}
        <div className="mt-2 flex flex-wrap gap-4 text-xs text-slate-500">
          <span>Attempt {task.attempt}</span>
          {task.assigned_agent_name && <span>Agent: {task.assigned_agent_name}</span>}
          {task.branch && <span>Branch: {task.branch}</span>}
          {task.pr_url && (
            <a href={task.pr_url} target="_blank" rel="noreferrer" className="text-indigo-400 hover:underline">
              Pull Request
            </a>
          )}
        </div>
      </div>

      {filesModified.length > 0 && (
        <Panel title="Files Changed">
          <div className="flex flex-col gap-2">
            {filesModified.map((a) => (
              <pre key={a.id} className="overflow-x-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                {a.content}
              </pre>
            ))}
          </div>
        </Panel>
      )}

      {diffs.length > 0 && (
        <Panel title="Diff">
          <div className="flex flex-col gap-2">
            {diffs.map((a) => (
              <pre key={a.id} className="max-h-96 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                {a.content}
              </pre>
            ))}
          </div>
        </Panel>
      )}

      {diffs.length === 0 && filesModified.length === 0 && (
        <div className="text-sm text-slate-500">No diff captured for this task yet.</div>
      )}

      <Panel title="Model Runs">
        {model_runs.length === 0 ? (
          <div className="text-sm text-slate-500">No model runs recorded.</div>
        ) : (
          <ul className="flex flex-col gap-2">
            {model_runs.map((run) => (
              <li key={run.id} className="rounded-md border border-slate-800 p-2 text-sm">
                <div className="flex items-center justify-between">
                  <span className="text-slate-200">{run.provider}</span>
                  <span className={run.status === "failed" ? "text-rose-400" : "text-slate-500"}>{run.status}</span>
                </div>
                {run.summary && <p className="mt-1 text-xs text-slate-400">{run.summary}</p>}
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <Panel title="Logs">
        {timeline.length === 0 ? (
          <div className="text-sm text-slate-500">No events recorded.</div>
        ) : (
          <ul className="flex flex-col gap-1">
            {timeline.map((e) => (
              <li key={e.id} className="flex items-center justify-between text-sm">
                <span className="text-slate-300">{humanizeEventType(e.event_type)}</span>
                <span className="text-xs text-slate-500">{formatRelativeTime(e.timestamp)}</span>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}
