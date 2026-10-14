import type { ReactNode } from "react";
import { formatRelativeTime } from "../../lib/format";
import type { Agent, DecisionSummary, PlanningRun, Review, Run, Task } from "../../lib/types";

/// Every node kind the graph can show -- the drawer below the canvas
/// switches on `kind` to render the right detail for whatever was last
/// clicked. `planning_run` carries the task it explains alongside the run
/// itself, since "why was THIS task created" lives on the Task
/// (`creation_reason`), not on the PlanningRun. `decision` uses the
/// cross-project `DecisionSummary` shape -- the same one `getDecisions()`
/// already returns -- rather than fetching a second, fuller record.
export type SelectedNode =
  | { kind: "planning_run"; data: PlanningRun; forTask: Task }
  | { kind: "task"; data: Task }
  | { kind: "agent"; data: Agent }
  | { kind: "run"; data: Run }
  | { kind: "review"; data: Review }
  | { kind: "decision"; data: DecisionSummary };

export function TaskGraphDrawer({ selected }: { selected: SelectedNode | null }) {
  if (!selected) {
    return (
      <div className="rounded-md border border-slate-800 bg-slate-900/40 p-4 text-sm text-slate-500">
        Click any node above to see its detail.
      </div>
    );
  }

  return (
    <div className="rounded-md border border-slate-800 bg-slate-900/40 p-4">
      <div className="mb-3 flex items-center justify-between text-[11px] uppercase tracking-wide text-slate-500">
        <span>{drawerTitle(selected)}</span>
        <span>click any node above to swap this panel</span>
      </div>
      <DrawerBody selected={selected} />
    </div>
  );
}

function drawerTitle(selected: SelectedNode): string {
  switch (selected.kind) {
    case "planning_run":
      return `Planning Run #${selected.data.sequence}`;
    case "task":
      return "Task";
    case "agent":
      return `Agent · ${selected.data.name}`;
    case "run":
      return `Run · attempt ${selected.data.attempt} · ${selected.data.provider}`;
    case "review":
      return "Review";
    case "decision":
      return "Decision";
  }
}

function DrawerBody({ selected }: { selected: SelectedNode }) {
  switch (selected.kind) {
    case "planning_run": {
      const { data, forTask } = selected;
      return (
        <div className="flex flex-col gap-3 text-sm">
          {forTask.creation_reason && <Field label="Why this task was created">{forTask.creation_reason}</Field>}
          <Field label="Summary">
            <pre className="whitespace-pre-wrap text-xs text-slate-300">{data.summary}</pre>
          </Field>
          <div className="flex flex-wrap gap-4 text-xs text-slate-500">
            <span>{data.task_count} tasks generated</span>
            <span>{data.criteria_coverage_percent.toFixed(0)}% criteria coverage</span>
            <span>{formatRelativeTime(data.started_at)}</span>
          </div>
        </div>
      );
    }
    case "task": {
      const t = selected.data;
      return (
        <div className="flex flex-col gap-3 text-sm">
          {t.description && <Field label="Description">{t.description}</Field>}
          {t.success_criteria.length > 0 && (
            <Field label="Success Criteria">
              <ul className="list-disc pl-4 text-xs text-slate-300">
                {t.success_criteria.map((c, i) => (
                  <li key={i}>{c}</li>
                ))}
              </ul>
            </Field>
          )}
          {t.success_criteria_reason && <Field label="Why these criteria">{t.success_criteria_reason}</Field>}
          {t.target_score_reason && (
            <Field label="Why this quality bar">{`Target ${t.target_score.toFixed(2)} — ${t.target_score_reason}`}</Field>
          )}
          {t.blocked_reason && <Field label="Blocked because">{t.blocked_reason}</Field>}
        </div>
      );
    }
    case "agent": {
      const a = selected.data;
      return (
        <div className="flex flex-col gap-3 text-sm">
          <Field label="Role">{`${a.role} — ${a.role_description}`}</Field>
          <Field label="Status">{a.status}</Field>
          {a.last_heartbeat && <Field label="Last heartbeat">{formatRelativeTime(a.last_heartbeat)}</Field>}
        </div>
      );
    }
    case "run": {
      const r = selected.data;
      return (
        <div className="flex flex-col gap-3 text-sm">
          {r.prompt && (
            <Field label="Prompt">
              <pre className="whitespace-pre-wrap text-xs text-slate-300">{r.prompt}</pre>
            </Field>
          )}
          {r.response && (
            <Field label="Response">
              <pre className="whitespace-pre-wrap text-xs text-emerald-400">{r.response}</pre>
            </Field>
          )}
          {r.summary && <Field label="Summary">{r.summary}</Field>}
          {!r.prompt && !r.response && !r.summary && (
            <div className="text-xs text-slate-500">No output recorded for this attempt yet.</div>
          )}
        </div>
      );
    }
    case "review": {
      const rv = selected.data;
      return (
        <div className="flex flex-col gap-3 text-sm">
          <Field label="Score">{rv.score.toFixed(2)}</Field>
          <Field label="Summary">{rv.summary}</Field>
          {rv.recommendations.length > 0 && (
            <Field label="Recommendations">
              <ul className="list-disc pl-4 text-xs text-slate-300">
                {rv.recommendations.map((rec, i) => (
                  <li key={i}>{rec}</li>
                ))}
              </ul>
            </Field>
          )}
        </div>
      );
    }
    case "decision": {
      const d = selected.data;
      return (
        <div className="flex flex-col gap-3 text-sm">
          <Field label="Question">{d.question}</Field>
          {d.context && <Field label="Context">{d.context}</Field>}
          {d.creation_reason && <Field label="Why this was escalated">{d.creation_reason}</Field>}
        </div>
      );
    }
  }
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">{label}</div>
      <div className="text-slate-300">{children}</div>
    </div>
  );
}
