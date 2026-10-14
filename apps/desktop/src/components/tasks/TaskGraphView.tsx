import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useElapsedLabel } from "../../lib/hooks";
import type { DecisionStatus, ReviewStatus, Run, RunStatus } from "../../lib/types";
import { AGENT_STATUS_COLOR, AGENT_STATUS_DOT } from "../agents/AgentTree";
import { TaskGraphDrawer, type SelectedNode } from "./TaskGraphDrawer";
import { TaskGraphEdge, TaskGraphNode } from "./TaskGraphNode";
import { displayStatus } from "./taskDisplay";

const RUN_CAP = 5;

const RUN_DOT: Record<RunStatus, string> = {
  running: "bg-indigo-400",
  completed: "bg-emerald-400",
  failed: "bg-rose-400",
};
const RUN_COLOR: Record<RunStatus, string> = {
  running: "text-indigo-300",
  completed: "text-emerald-400",
  failed: "text-rose-400",
};
const RUN_LABEL: Record<RunStatus, string> = { running: "running", completed: "done", failed: "failed" };

const REVIEW_DOT: Record<ReviewStatus, string> = {
  pending: "bg-slate-500",
  approved: "bg-emerald-400",
  needs_changes: "bg-rose-400",
};
const REVIEW_COLOR: Record<ReviewStatus, string> = {
  pending: "text-slate-500",
  approved: "text-emerald-400",
  needs_changes: "text-rose-400",
};
const REVIEW_LABEL: Record<ReviewStatus, string> = {
  pending: "Pending",
  approved: "Approved",
  needs_changes: "Needs changes",
};

const DECISION_DOT: Record<DecisionStatus, string> = { open: "bg-amber-400", resolved: "bg-emerald-400" };
const DECISION_COLOR: Record<DecisionStatus, string> = { open: "text-amber-400", resolved: "text-emerald-400" };

/// The per-task execution graph: Planning Run -> Task -> Agent -> Run (one
/// per attempt) -> Review -> Decision, left to right. Reuses the same
/// React Query + Tauri push-invalidation the rest of the app already has
/// (see main.tsx's `state-changed` listener) -- no bespoke polling here.
export function TaskGraphView({ taskId }: { taskId: string }) {
  const [selected, setSelected] = useState<SelectedNode | null>(null);
  const [showAllRuns, setShowAllRuns] = useState(false);

  const { data: detail } = useQuery({ queryKey: ["taskDetail", taskId], queryFn: () => api.getTaskDetail(taskId) });
  const task = detail?.task;

  const { data: planningRuns } = useQuery({
    queryKey: ["planningHistory", task?.project_id],
    queryFn: () => api.getPlanningHistory(task!.project_id),
    enabled: !!task?.project_id,
  });
  const planningRun = task?.created_by_planning_run_id
    ? planningRuns?.find((r) => r.run_id === task.created_by_planning_run_id) ?? null
    : null;

  const { data: agents } = useQuery({
    queryKey: ["agents", task?.project_id],
    queryFn: () => api.getAgents(task!.project_id),
    enabled: !!task?.project_id,
  });
  const agent = task?.assigned_agent_id ? agents?.find((a) => a.id === task.assigned_agent_id) ?? null : null;

  const { data: decisions } = useQuery({ queryKey: ["decisions"], queryFn: api.getDecisions });
  const taskDecisions = (decisions ?? []).filter((d) => d.task_id === taskId);

  if (!detail || !task) {
    return <div className="text-sm text-slate-500">Loading…</div>;
  }

  const runs = detail.runs;
  const visibleRuns = showAllRuns ? runs : runs.slice(-RUN_CAP);
  const hiddenRunCount = runs.length - visibleRuns.length;
  const taskStatus = displayStatus(task);

  return (
    <div className="flex flex-col gap-3">
      <div className="overflow-x-auto rounded-md border border-slate-800 bg-slate-950/40 p-4">
        <div className="flex w-max items-center">
          {planningRun && (
            <>
              <TaskGraphNode
                kicker={`Planning Run #${planningRun.sequence}`}
                title={planningRun.planned_task_title}
                statusLabel="created this task"
                dotColor="bg-emerald-400"
                statusColor="text-emerald-400"
                selected={selected?.kind === "planning_run"}
                onClick={() => setSelected({ kind: "planning_run", data: planningRun, forTask: task })}
              />
              <TaskGraphEdge />
            </>
          )}

          <TaskGraphNode
            kicker="Task"
            title={task.title}
            statusLabel={taskStatus.label}
            dotColor={taskStatus.dot}
            statusColor={taskStatus.color}
            selected={selected?.kind === "task"}
            onClick={() => setSelected({ kind: "task", data: task })}
          />
          <TaskGraphEdge />

          {agent ? (
            <TaskGraphNode
              kicker="Agent"
              title={agent.name}
              statusLabel={agent.status}
              dotColor={AGENT_STATUS_DOT[agent.status]}
              statusColor={AGENT_STATUS_COLOR[agent.status]}
              selected={selected?.kind === "agent"}
              onClick={() => setSelected({ kind: "agent", data: agent })}
            />
          ) : (
            <TaskGraphNode
              kicker="Agent"
              title="Unassigned"
              statusLabel="—"
              dotColor="bg-slate-600"
              statusColor="text-slate-500"
              selected={false}
              onClick={() => {}}
              disabled
            />
          )}
          <TaskGraphEdge />

          <div className="flex flex-col gap-2">
            {hiddenRunCount > 0 && (
              <button
                onClick={() => setShowAllRuns(true)}
                className="text-left text-[10px] text-slate-500 hover:text-slate-300"
              >
                +{hiddenRunCount} earlier attempt{hiddenRunCount === 1 ? "" : "s"}
              </button>
            )}
            {visibleRuns.length === 0 ? (
              <TaskGraphNode
                kicker="Run"
                title="Not started"
                statusLabel="—"
                dotColor="bg-slate-600"
                statusColor="text-slate-500"
                selected={false}
                onClick={() => {}}
                disabled
              />
            ) : (
              visibleRuns.map((run) => (
                <RunNode
                  key={run.id}
                  run={run}
                  selected={selected?.kind === "run" && selected.data.id === run.id}
                  onSelect={() => setSelected({ kind: "run", data: run })}
                />
              ))
            )}
          </div>
          <TaskGraphEdge />

          <div className="flex flex-col gap-2">
            {detail.reviews.length === 0 ? (
              <TaskGraphNode
                kicker="Review"
                title="Pending"
                statusLabel={runs.length === 0 ? "—" : "waiting on latest attempt"}
                dotColor="bg-slate-600"
                statusColor="text-slate-500"
                selected={false}
                onClick={() => {}}
                disabled
              />
            ) : (
              detail.reviews.map((review) => (
                <TaskGraphNode
                  key={review.id}
                  kicker="Review"
                  title={REVIEW_LABEL[review.status]}
                  statusLabel={`score ${review.score.toFixed(2)}`}
                  dotColor={REVIEW_DOT[review.status]}
                  statusColor={REVIEW_COLOR[review.status]}
                  selected={selected?.kind === "review" && selected.data.id === review.id}
                  onClick={() => setSelected({ kind: "review", data: review })}
                />
              ))
            )}
          </div>
          <TaskGraphEdge />

          <div className="flex flex-col gap-2">
            {taskDecisions.length === 0 ? (
              <TaskGraphNode
                kicker="Decision"
                title="None yet"
                statusLabel="—"
                dotColor="bg-slate-600"
                statusColor="text-slate-500"
                selected={false}
                onClick={() => {}}
                disabled
              />
            ) : (
              taskDecisions.map((d) => (
                <TaskGraphNode
                  key={d.id}
                  kicker="Decision"
                  title={d.status === "open" ? "Awaiting answer" : "Resolved"}
                  statusLabel={d.question}
                  dotColor={DECISION_DOT[d.status]}
                  statusColor={DECISION_COLOR[d.status]}
                  selected={selected?.kind === "decision" && selected.data.id === d.id}
                  onClick={() => setSelected({ kind: "decision", data: d })}
                />
              ))
            )}
          </div>
        </div>
      </div>

      <TaskGraphDrawer selected={selected} />
    </div>
  );
}

function RunNode({ run, selected, onSelect }: { run: Run; selected: boolean; onSelect: () => void }) {
  const elapsed = useElapsedLabel(run.started_at);
  const duration = run.status === "running" ? elapsed : staticDuration(run);

  return (
    <TaskGraphNode
      kicker={`Run · attempt ${run.attempt}`}
      title={run.provider}
      statusLabel={`${RUN_LABEL[run.status]} — ${duration}`}
      dotColor={RUN_DOT[run.status]}
      statusColor={RUN_COLOR[run.status]}
      selected={selected}
      onClick={onSelect}
    />
  );
}

function staticDuration(run: Run): string {
  if (!run.completed_at) return "—";
  const totalSeconds = Math.max(0, Math.round((Date.parse(run.completed_at) - Date.parse(run.started_at)) / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return minutes > 0 ? `${minutes}m${seconds}s` : `${seconds}s`;
}
