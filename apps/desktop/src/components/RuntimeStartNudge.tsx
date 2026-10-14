import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAction } from "../lib/hooks";
import { ActionButton } from "./ActionButton";

/// Tasks only ever get worked on while the global daemon is ticking --
/// without it, a planned project's Ready tasks just sit there with no one
/// picking them up. Surfaced wherever that's most likely to be missed:
/// Mission Control (the "is work progressing" home screen) and a project's
/// own detail page.
export function RuntimeStartNudge() {
  const { data: status } = useQuery({ queryKey: ["runtimeStatus"], queryFn: api.runtimeStatus });
  const start = useAction<void, void>(api.runtimeStart, {
    successMessage: "Runtime started",
    invalidateKeys: [["runtimeStatus"]],
  });

  if (!status || status.daemon_running) return null;

  return (
    <div className="flex items-center justify-between rounded-md border border-amber-700/50 bg-amber-950/30 px-3 py-2 text-sm text-amber-300">
      <span>The runtime isn't running -- planned tasks won't be picked up until it starts.</span>
      <ActionButton label="Start Runtime" onClick={() => start.mutate()} pending={start.isPending} />
    </div>
  );
}
