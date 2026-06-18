import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Panel } from "../Panel";

export function RuntimeStatusPanel() {
  const { data } = useQuery({ queryKey: ["runtimeStatus"], queryFn: api.runtimeStatus });

  return (
    <Panel title="Runtime Status">
      {data ? (
        <div className="grid grid-cols-2 gap-3 text-sm">
          <Stat
            label="Runtime"
            value={data.daemon_running ? "Running" : data.stale_pidfile ? "Stale" : "Stopped"}
            tone={data.daemon_running ? "good" : data.stale_pidfile ? "warn" : "neutral"}
          />
          <Stat label="Agents" value={String(data.active_agent_count)} />
          <Stat label="Tasks" value={String(data.active_task_count)} />
          <Stat label="Inbox" value={String(data.open_inbox_count)} />
        </div>
      ) : (
        <div className="text-sm text-slate-500">Loading…</div>
      )}
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
    tone === "good" ? "text-emerald-400" : tone === "warn" ? "text-amber-400" : "text-slate-100";
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className={`text-lg font-semibold ${color}`}>{value}</div>
    </div>
  );
}
