import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAction, useElapsedLabel } from "../lib/hooks";
import { ActionButton } from "../components/ActionButton";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Panel } from "../components/Panel";
import type { ActiveExecution } from "../lib/types";

export function RuntimePage() {
  const [confirmStop, setConfirmStop] = useState(false);
  const { data: status } = useQuery({ queryKey: ["runtimeStatus"], queryFn: api.runtimeStatus });
  const { data: active } = useQuery({ queryKey: ["activeExecution"], queryFn: api.getActiveExecution });

  const invalidateKeys = [["runtimeStatus"], ["activeExecution"]];
  const start = useAction<void, void>(api.runtimeStart, { successMessage: "Runtime started", invalidateKeys });
  const stop = useAction<void, void>(api.runtimeStop, { successMessage: "Runtime stopped", invalidateKeys });
  const restart = useAction<void, void>(api.runtimeRestart, {
    successMessage: "Runtime restarted",
    invalidateKeys,
  });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold text-slate-100">Runtime</h1>
        <div className="flex items-center gap-2">
          {!status?.daemon_running && (
            <ActionButton label="Start" onClick={() => start.mutate()} pending={start.isPending} />
          )}
          {status?.daemon_running && (
            <ActionButton label="Stop" tone="danger" onClick={() => setConfirmStop(true)} />
          )}
          <ActionButton label="Restart" onClick={() => restart.mutate()} pending={restart.isPending} />
        </div>
      </div>

      <Panel title="Status">
        {status ? (
          <div className="grid grid-cols-2 gap-3 text-sm md:grid-cols-4">
            <Stat
              label="Runtime"
              value={status.daemon_running ? "Running" : status.stale_pidfile ? "Stale" : "Stopped"}
            />
            <Stat label="PID" value={status.pid ? String(status.pid) : "—"} />
            <Stat label="Agents" value={String(status.active_agent_count)} />
            <Stat label="Tasks" value={String(status.active_task_count)} />
            <Stat label="Inbox" value={String(status.open_inbox_count)} />
            <Stat label="Last Started" value={status.last_started ?? "—"} />
            <Stat label="Last Stopped" value={status.last_stopped ?? "—"} />
          </div>
        ) : (
          <div className="text-sm text-slate-500">Loading…</div>
        )}
      </Panel>

      <Panel title="Active Executions">
        {!active || active.length === 0 ? (
          <div className="text-sm text-slate-500">No tasks in progress.</div>
        ) : (
          <ul className="flex flex-col gap-2">
            {active.map((item) => (
              <ActiveExecutionRow key={item.task_id} item={item} />
            ))}
          </ul>
        )}
      </Panel>

      <ConfirmDialog
        open={confirmStop}
        title="Stop the runtime?"
        description="Stopping halts all tick processing -- agents will not be assigned new work or make progress on in-flight tasks until the runtime is started again."
        confirmLabel="Stop Runtime"
        danger
        pending={stop.isPending}
        onConfirm={() => stop.mutate(undefined, { onSuccess: () => setConfirmStop(false) })}
        onCancel={() => setConfirmStop(false)}
      />
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className="text-lg font-semibold text-slate-100">{value}</div>
    </div>
  );
}

function ActiveExecutionRow({ item }: { item: ActiveExecution }) {
  const elapsed = useElapsedLabel(item.started_at);
  return (
    <li className="flex items-center justify-between rounded-md border border-slate-800 bg-slate-900/60 px-3 py-2 text-sm">
      <div>
        <div className="font-medium text-slate-200">{item.task_title}</div>
        <div className="text-xs text-slate-500">
          {item.agent_name} · {item.project_slug}
        </div>
      </div>
      <div className="text-right">
        <div className="text-xs font-semibold text-emerald-400">Running</div>
        <div className="text-xs text-slate-500">{elapsed}</div>
      </div>
    </li>
  );
}
