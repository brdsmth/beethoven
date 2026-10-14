import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import { ActionButton } from "../ActionButton";
import { ConfirmDialog } from "../ConfirmDialog";
import { Panel } from "../Panel";
import type { StopAllReport } from "../../lib/types";

/// Promoted above the fold on Home -- the daemon's on/off state and what
/// the scheduler is doing across every project is the single most
/// consequential thing in the app (nothing executes without it), so it
/// shouldn't be buried in the last panel of a grid. Absorbs what used to
/// be RuntimePage's separate Status/Scheduler panels, since those numbers
/// had no per-project equivalent and would otherwise be lost.
///
/// The "Agents" stat and "Stop All Agents" control live here too, not in a
/// separate panel: the user explicitly asked to be able to trust this
/// number and to have one global kill switch, and this is already the most
/// visible status surface on Home.
export function RuntimeStatusPanel() {
  const { data } = useQuery({ queryKey: ["runtimeStatus"], queryFn: api.runtimeStatus });
  const [confirmStopAll, setConfirmStopAll] = useState(false);

  const stopAll = useAction<void, StopAllReport>(api.stopAllAgents, {
    successMessage: (_v, r) => `Stopped ${r.interrupted_count} agent${r.interrupted_count === 1 ? "" : "s"}, swept ${r.orphans_swept} orphaned session${r.orphans_swept === 1 ? "" : "s"}`,
    invalidateKeys: [["runtimeStatus"], ["activeExecution"], ["workspaces"]],
  });

  const unverified = data ? data.ghost_session_count + data.orphaned_session_count : 0;

  return (
    <Panel title="Runtime Status">
      {data ? (
        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-end">
            <ActionButton
              label="Stop All Agents"
              tone="danger"
              disabled={data.verified_agent_count === 0 && unverified === 0}
              onClick={() => setConfirmStopAll(true)}
            />
          </div>
          <div className="grid grid-cols-2 gap-3 text-sm md:grid-cols-4">
            <Stat
              label="Runtime"
              value={data.daemon_running ? "Running" : data.stale_pidfile ? "Stale" : "Stopped"}
              tone={data.daemon_running ? "good" : data.stale_pidfile ? "warn" : "neutral"}
            />
            <Stat
              label="Agents"
              value={unverified === 0 ? String(data.verified_agent_count) : `${data.verified_agent_count} (${unverified} unverified)`}
              tone={unverified === 0 ? "neutral" : "warn"}
            />
            <Stat label="Workers" value={String(data.worker_count)} />
            <Stat label="Inbox" value={String(data.open_inbox_count)} />
          </div>
          <div className="grid grid-cols-2 gap-3 text-sm md:grid-cols-4">
            <Stat label="Queue Depth" value={String(data.queue_depth)} />
            <Stat label="Ready" value={String(data.ready_task_count)} />
            <Stat label="Blocked" value={String(data.blocked_task_count)} tone="warn" />
            <Stat label="Failed" value={String(data.failed_task_count)} tone="warn" />
          </div>
        </div>
      ) : (
        <div className="text-sm text-slate-500">Loading…</div>
      )}

      <ConfirmDialog
        open={confirmStopAll}
        title="Stop all agents?"
        description="Kills every running agent session across every project right now, plus any stray session not tied to a tracked task. Affected tasks go back to Ready so they can be picked up again."
        confirmLabel="Stop All Agents"
        danger
        pending={stopAll.isPending}
        onConfirm={() => stopAll.mutate(undefined, { onSuccess: () => setConfirmStopAll(false) })}
        onCancel={() => setConfirmStopAll(false)}
      />
    </Panel>
  );
}

function Stat({
  label,
  value,
  tone = "neutral",
}: {
  label: string;
  value: string;
  tone?: "good" | "warn" | "neutral";
}) {
  const color =
    tone === "good" ? "text-emerald-400" : tone === "warn" && value !== "0" ? "text-amber-400" : "text-slate-100";
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className={`text-lg font-semibold ${color}`}>{value}</div>
    </div>
  );
}
