import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { Panel } from "../components/Panel";

export function SettingsPage() {
  const { data: homeDir } = useQuery({ queryKey: ["homeDir"], queryFn: api.getHomeDir });

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Settings</h1>
      <Panel title="Beethoven Home">
        <div className="font-mono text-sm text-slate-300">{homeDir ?? "Loading…"}</div>
      </Panel>
    </div>
  );
}
