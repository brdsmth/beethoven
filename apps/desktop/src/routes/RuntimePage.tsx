import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAction, useElapsedLabel, useSelectedProject } from "../lib/hooks";
import { ActionButton } from "../components/ActionButton";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Panel } from "../components/Panel";
import { Tabs } from "../components/Tabs";
import { ProjectPicker } from "../components/ProjectPicker";
import { AgentTree } from "../components/agents/AgentTree";
import { AgentDetail } from "../components/agents/AgentDetail";
import { EventStream } from "../components/timeline/EventStream";
import { formatRelativeTime } from "../lib/format";
import type { ActiveExecution, AgentHealth, AgentHealthStatus } from "../lib/types";

type RuntimeTab = "overview" | "workers" | "logs";
const TABS: { id: RuntimeTab; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "workers", label: "Workers" },
  { id: "logs", label: "Logs" },
];

/// Workers are execution capacity, not a primary concept -- this is where
/// they (and the raw event log) live now: nested under Runtime, a tab away
/// from the scheduler-level numbers that actually answer "is work moving."
export function RuntimePage() {
  const [confirmStop, setConfirmStop] = useState(false);
  const [tab, setTab] = useState<RuntimeTab>("overview");
  const { data: status } = useQuery({ queryKey: ["runtimeStatus"], queryFn: api.runtimeStatus });

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

      <Tabs tabs={TABS} active={tab} onChange={setTab} />

      {tab === "overview" && <OverviewTab />}
      {tab === "workers" && <WorkersTab />}
      {tab === "logs" && <LogsTab />}

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

function OverviewTab() {
  const { data: status } = useQuery({ queryKey: ["runtimeStatus"], queryFn: api.runtimeStatus });
  const { data: active } = useQuery({ queryKey: ["activeExecution"], queryFn: api.getActiveExecution });

  return (
    <div className="flex flex-col gap-4">
      <Panel title="Status">
        {status ? (
          <div className="grid grid-cols-2 gap-3 text-sm md:grid-cols-4">
            <Stat
              label="Runtime"
              value={status.daemon_running ? "Running" : status.stale_pidfile ? "Stale" : "Stopped"}
            />
            <Stat label="PID" value={status.pid ? String(status.pid) : "—"} />
            <Stat label="Inbox" value={String(status.open_inbox_count)} />
            <Stat label="Last Started" value={status.last_started ?? "—"} />
          </div>
        ) : (
          <div className="text-sm text-slate-500">Loading…</div>
        )}
      </Panel>

      <Panel title="Scheduler">
        {status ? (
          <div className="grid grid-cols-2 gap-3 text-sm md:grid-cols-3">
            <Stat label="Workers Active" value={String(status.worker_count)} />
            <Stat label="Queue Depth" value={String(status.queue_depth)} />
            <Stat label="Ready" value={String(status.ready_task_count)} />
            <Stat label="Running" value={String(status.active_task_count)} />
            <Stat label="Blocked" value={String(status.blocked_task_count)} tone="warn" />
            <Stat label="Failed" value={String(status.failed_task_count)} tone="warn" />
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
    </div>
  );
}

function WorkersTab() {
  const { projects, selectedProjectId, setSelectedProjectId } = useSelectedProject();
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);

  const { data: agents } = useQuery({
    queryKey: ["agents", selectedProjectId],
    queryFn: () => api.getAgents(selectedProjectId as string),
    enabled: !!selectedProjectId,
  });
  const { data: health } = useQuery({ queryKey: ["agentHealth"], queryFn: api.getAgentHealth });

  const selectedAgent = (agents ?? []).find((a) => a.id === selectedAgentId) ?? null;
  const attention = (health ?? []).filter((a) => a.health !== "healthy");

  if (!selectedProjectId) {
    return <div className="text-sm text-slate-500">No projects yet.</div>;
  }

  return (
    <div className="flex flex-col gap-4">
      {attention.length > 0 && (
        <Panel title="Needs Attention">
          <ul className="flex flex-col gap-2">
            {attention.map((a) => (
              <WorkerHealthRow key={a.agent_id} agent={a} />
            ))}
          </ul>
        </Panel>
      )}

      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-400">Worker Pool</h2>
        <ProjectPicker projects={projects} selectedProjectId={selectedProjectId} onChange={setSelectedProjectId} />
      </div>

      <div className="grid grid-cols-2 gap-4">
        <Panel title="Organization">
          <AgentTree agents={agents ?? []} selectedAgentId={selectedAgentId} onSelect={setSelectedAgentId} />
        </Panel>

        {selectedAgent ? (
          <AgentDetail agent={selectedAgent} />
        ) : (
          <Panel title="Agent Detail">
            <div className="text-sm text-slate-500">Select an agent to see details.</div>
          </Panel>
        )}
      </div>
    </div>
  );
}

const HEALTH_COLOR: Record<AgentHealthStatus, string> = {
  healthy: "text-emerald-400",
  slow: "text-amber-400",
  stalled: "text-rose-400",
};

function WorkerHealthRow({ agent }: { agent: AgentHealth }) {
  return (
    <li className="flex items-center justify-between text-sm">
      <div>
        <span className="text-slate-200">{agent.agent_name}</span>
        {agent.current_task_title && <span className="ml-2 text-xs text-slate-500">{agent.current_task_title}</span>}
      </div>
      <div className="flex items-center gap-3 text-xs">
        <span className="text-slate-500">
          {agent.last_heartbeat ? `heartbeat ${formatRelativeTime(agent.last_heartbeat)}` : "no heartbeat"}
        </span>
        <span className={`font-semibold uppercase ${HEALTH_COLOR[agent.health]}`}>{agent.health}</span>
      </div>
    </li>
  );
}

function LogsTab() {
  return <EventStream />;
}

function Stat({ label, value, tone = "neutral" }: { label: string; value: string; tone?: "good" | "warn" | "neutral" }) {
  const color = tone === "good" ? "text-emerald-400" : tone === "warn" && value !== "0" ? "text-amber-400" : "text-slate-100";
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className={`text-lg font-semibold ${color}`}>{value}</div>
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
