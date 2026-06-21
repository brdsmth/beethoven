import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { formatRelativeTime, humanizeEventType } from "../lib/format";
import { useViewStore } from "../lib/store";
import { Panel } from "./Panel";

/**
 * Shared read-only view over one task's full execution history -- backs
 * both the "View Diff" and "View Logs" actions, since both are just
 * different slices of the same already-stored model runs/artifacts/timeline.
 */
export function TaskDetailPanel({ taskId }: { taskId: string }) {
  const isOperator = useViewStore((s) => s.mode) === "operator";
  const { data, isLoading } = useQuery({
    queryKey: ["taskDetail", taskId],
    queryFn: () => api.getTaskDetail(taskId),
  });
  const { data: planningHistory } = useQuery({
    queryKey: ["planningHistory", data?.task.project_id],
    queryFn: () => api.getPlanningHistory(data!.task.project_id),
    enabled: !!data,
  });

  if (isLoading || !data) {
    return <div className="text-sm text-slate-500">Loading…</div>;
  }

  const { task, runs, reviews, artifacts, timeline } = data;
  const diffs = artifacts.filter((a) => a.kind === "diff");
  const filesModified = artifacts.filter((a) => a.kind === "files_modified");
  const planningRun = (planningHistory ?? []).find((r) => r.run_id === task.created_by_planning_run_id);

  return (
    <div className="flex flex-col gap-4">
      <div>
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold text-slate-100">{task.title}</h2>
          <span className="text-xs uppercase text-slate-500">{task.status}</span>
        </div>
        {task.description && <p className="mt-1 text-sm text-slate-400">{task.description}</p>}
        {task.status === "blocked" && task.blocked_reason && (
          <p className="mt-1 text-sm text-rose-400">{task.blocked_reason}</p>
        )}
        {task.creation_reason && (
          <p className="mt-1 text-xs text-slate-500">
            Created by: {planningRun ? `Planning Run #${planningRun.sequence}` : "Planning"} -- {task.creation_reason}
          </p>
        )}
        <div className="mt-2 flex flex-wrap gap-4 text-xs text-slate-500">
          <span>Attempt {task.attempt}</span>
          <span>Target Score {task.target_score}</span>
          {task.assigned_agent_name && <span>Agent: {task.assigned_agent_name}</span>}
          {task.branch && <span>Branch: {task.branch}</span>}
          {task.pr_url && (
            <a href={task.pr_url} target="_blank" rel="noreferrer" className="text-indigo-400 hover:underline">
              Pull Request
            </a>
          )}
        </div>
      </div>

      {task.success_criteria.length > 0 && (
        <Panel title="Success Criteria">
          <ul className="list-inside list-disc text-sm text-slate-300">
            {task.success_criteria.map((criterion, idx) => <li key={idx}>{criterion}</li>)}
          </ul>
          {task.success_criteria_reason && (
            <p className="mt-2 text-xs text-slate-500">
              Reason: {task.success_criteria_reason}
              {planningRun && ` (Planning Run #${planningRun.sequence})`}
            </p>
          )}
        </Panel>
      )}

      {reviews.length > 0 && (
        <Panel title="Reviews">
          <ul className="flex flex-col gap-2">
            {reviews.map((r) => (
              <li key={r.id} className="rounded-md border border-slate-800 p-2 text-sm">
                <div className="flex items-center justify-between">
                  <span className="text-slate-200">Score {r.score}</span>
                  <span className="text-xs uppercase text-slate-500">{r.status}</span>
                </div>
                {r.summary && <p className="mt-1 text-xs text-slate-400">{r.summary}</p>}
                {r.recommendations.length > 0 && (
                  <ul className="mt-1 list-inside list-disc text-xs text-slate-500">
                    {r.recommendations.map((rec, idx) => <li key={idx}>{rec}</li>)}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        </Panel>
      )}

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

      <Panel title="Runs">
        {runs.length === 0 ? (
          <div className="text-sm text-slate-500">No runs recorded.</div>
        ) : (
          <ul className="flex flex-col gap-2">
            {runs.map((run) => (
              <li key={run.id} className="rounded-md border border-slate-800 p-2 text-sm">
                <div className="flex items-center justify-between">
                  <span className="text-slate-200">
                    {run.provider} · attempt {run.attempt}
                  </span>
                  <span className={run.status === "failed" ? "text-rose-400" : "text-slate-500"}>{run.status}</span>
                </div>
                {run.summary && <p className="mt-1 text-xs text-slate-400">{run.summary}</p>}
              </li>
            ))}
          </ul>
        )}
      </Panel>

      {isOperator &&
        runs.map(
          (run) =>
            (run.prompt || run.response) && (
              <div key={run.id} className="flex flex-col gap-4">
                {run.prompt && (
                  <Panel title={`Prompt (${run.provider})`}>
                    <pre className="max-h-96 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                      {run.prompt}
                    </pre>
                  </Panel>
                )}
                {run.response && (
                  <Panel title={`Claude Response (${run.provider})`}>
                    <pre className="max-h-96 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                      {run.response}
                    </pre>
                  </Panel>
                )}
              </div>
            ),
        )}

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
