import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAction } from "../lib/hooks";
import { ActionButton } from "../components/ActionButton";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Panel } from "../components/Panel";

export function SettingsPage() {
  const { data: homeDir } = useQuery({ queryKey: ["homeDir"], queryFn: api.getHomeDir });
  const { data: status } = useQuery({ queryKey: ["runtimeStatus"], queryFn: api.runtimeStatus });
  const [confirmStop, setConfirmStop] = useState(false);

  const invalidateKeys = [["runtimeStatus"], ["activeExecution"]];
  const start = useAction<void, void>(api.runtimeStart, { successMessage: "Runtime started", invalidateKeys });
  const stop = useAction<void, void>(api.runtimeStop, { successMessage: "Runtime stopped", invalidateKeys });
  const restart = useAction<void, void>(api.runtimeRestart, {
    successMessage: "Runtime restarted",
    invalidateKeys,
  });

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Settings</h1>

      <Panel title="Runtime">
        <div className="flex items-center justify-between">
          <div className="text-sm text-slate-400">
            The background daemon that ticks every project. Glanceable status lives on Home --
            these are the occasional, deliberate controls.
          </div>
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
      </Panel>

      <Panel title="Beethoven Home">
        <div className="font-mono text-sm text-slate-300">{homeDir ?? "Loading…"}</div>
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
