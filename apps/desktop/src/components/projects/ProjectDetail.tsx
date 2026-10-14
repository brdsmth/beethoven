import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { revealItemInDir } from "@tauri-apps/plugin-opener";
import { api } from "../../lib/api";
import { pickDirectory } from "../../lib/dialog";
import { useAction, useTabParam } from "../../lib/hooks";
import { pushToast } from "../../lib/toast";
import { formatRelativeTime } from "../../lib/format";
import { allTasks, computeProjectHealth, currentBottleneck } from "../../lib/projectHealth";
import { PROJECT_STATUS_TEXT_COLOR } from "../../lib/projectDisplay";
import { displayStatus } from "../tasks/taskDisplay";
import { DependencyGraph } from "../tasks/DependencyGraph";
import { NewGoalForm } from "../tasks/NewGoalForm";
import { TaskTree } from "../tasks/TaskTree";
import { PlanningSummaryPanel } from "../tasks/PlanningSummaryPanel";
import { PlanningHistoryPanel } from "../tasks/PlanningHistoryPanel";
import { AgentTree } from "../agents/AgentTree";
import { AgentDetail } from "../agents/AgentDetail";
import { EventStream } from "../timeline/EventStream";
import { ProjectChatPanel } from "./ProjectChatPanel";
import type { AgentHealthStatus, Decision, ProjectDetail as ProjectDetailData } from "../../lib/types";
import { ActionButton } from "../ActionButton";
import { ConfirmDialog } from "../ConfirmDialog";
import { DiffView } from "../DiffView";
import { EmptyState } from "../EmptyState";
import { InboxCardShell, RespondField } from "../inbox/InboxCardShell";
import { Panel } from "../Panel";
import { RuntimeStartNudge } from "../RuntimeStartNudge";
import { Tabs } from "../Tabs";
import { GoalHeader } from "../mission-control/GoalHeader";

type ProjectTab = "overview" | "tasks" | "decisions" | "artifacts" | "agents" | "chat" | "logs";
const TABS: { id: ProjectTab; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "tasks", label: "Tasks" },
  { id: "decisions", label: "Decisions" },
  { id: "artifacts", label: "Artifacts" },
  { id: "agents", label: "Agents" },
  { id: "chat", label: "Chat" },
  { id: "logs", label: "Logs" },
];
const TAB_IDS = TABS.map((t) => t.id);

export function ProjectDetail({ project }: { project: ProjectDetailData }) {
  const [confirmArchive, setConfirmArchive] = useState(false);
  const [tab, setTab] = useTabParam<ProjectTab>(TAB_IDS, "overview");

  const invalidateKeys = [["projects"], ["project", project.id]];
  const pause = useAction(api.pauseProject, { successMessage: `${project.name} paused`, invalidateKeys });
  const resume = useAction(api.resumeProject, { successMessage: `${project.name} resumed`, invalidateKeys });
  const archive = useAction(api.archiveProject, { successMessage: `${project.name} archived`, invalidateKeys });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold text-slate-100">{project.name}</h1>
          <div className="text-xs text-slate-500">{project.slug}</div>
        </div>
        <div className="flex items-center gap-3">
          <span className={PROJECT_STATUS_TEXT_COLOR[project.status]}>{project.status}</span>
          {project.status === "active" && (
            <ActionButton label="Pause" onClick={() => pause.mutate(project.id)} pending={pause.isPending} />
          )}
          {project.status === "paused" && (
            <ActionButton label="Resume" onClick={() => resume.mutate(project.id)} pending={resume.isPending} />
          )}
          {project.status !== "archived" && (
            <ActionButton label="Archive" tone="danger" onClick={() => setConfirmArchive(true)} />
          )}
        </div>
      </div>

      <RuntimeStartNudge />

      <Tabs tabs={TABS} active={tab} onChange={setTab} />

      {tab === "overview" && <OverviewTab project={project} />}
      {tab === "tasks" && <TasksTab project={project} />}
      {tab === "decisions" && <DecisionsTab project={project} />}
      {tab === "artifacts" && <ArtifactsTab project={project} />}
      {tab === "agents" && <AgentsTab project={project} />}
      {tab === "chat" && <ProjectChatPanel project={project} />}
      {tab === "logs" && <EventStream projectId={project.id} />}

      <ConfirmDialog
        open={confirmArchive}
        title={`Archive ${project.name}?`}
        description="Archived projects are removed from active execution entirely -- no further task assignment or work continues, even for tasks already in flight."
        confirmLabel="Archive"
        danger
        pending={archive.isPending}
        onConfirm={() => archive.mutate(project.id, { onSuccess: () => setConfirmArchive(false) })}
        onCancel={() => setConfirmArchive(false)}
      />
    </div>
  );
}

function OverviewTab({ project }: { project: ProjectDetailData }) {
  const health = computeProjectHealth(project);
  const bottleneck = currentBottleneck(project);
  const editable = project.status === "paused";

  const [repositoryPath, setRepositoryPath] = useState(project.repository_path);
  const [defaultBranch, setDefaultBranch] = useState(project.default_branch);

  const invalidateKeys = [["projects"], ["project", project.id]];
  const updatePath = useAction(
    ({ projectId, repositoryPath }: { projectId: string; repositoryPath: string }) =>
      api.updateProjectRepositoryPath(projectId, repositoryPath),
    { successMessage: "Repository path updated", invalidateKeys },
  );
  const updateBranch = useAction(
    ({ projectId, defaultBranch }: { projectId: string; defaultBranch: string }) =>
      api.updateProjectDefaultBranch(projectId, defaultBranch),
    { successMessage: "Default branch updated", invalidateKeys },
  );

  return (
    <div className="flex flex-col gap-4">
      <GoalHeader projectId={project.id} />

      <Panel title="Current Bottleneck">
        {bottleneck ? (
          <div>
            <div className="text-sm font-medium text-slate-200">{bottleneck.title}</div>
            <div className={`mt-1 text-xs font-semibold uppercase ${displayStatus(bottleneck).color}`}>
              {displayStatus(bottleneck).label}
            </div>
          </div>
        ) : (
          <div className="text-sm text-emerald-400">Nothing is currently holding this project up.</div>
        )}
      </Panel>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Running" value={health.runningTasks} />
        <Stat label="Blocked" value={health.blockedTasks} tone={health.blockedTasks > 0 ? "warn" : "neutral"} />
        <Stat label="Pending Decisions" value={health.decisionQueue} tone={health.decisionQueue > 0 ? "warn" : "neutral"} />
        <Stat label="Open Objectives" value={health.openObjectives} />
      </div>

      <PlanningSummaryPanel projectId={project.id} />

      <Panel title="Project Settings">
        <div className="flex flex-col gap-4">
          {!editable && <div className="text-xs text-slate-500">Pause the project to edit.</div>}

          <div className="flex flex-col gap-1">
            <label className="text-xs uppercase tracking-wide text-slate-500">Repository Path</label>
            {editable ? (
              <div className="flex items-center gap-2">
                <input
                  value={repositoryPath}
                  onChange={(e) => setRepositoryPath(e.target.value)}
                  className="flex-1 rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-sm text-slate-200"
                />
                <button
                  type="button"
                  onClick={async () => {
                    const picked = await pickDirectory("Choose Repository", repositoryPath || undefined);
                    if (picked) setRepositoryPath(picked);
                  }}
                  className="whitespace-nowrap rounded-md border border-slate-700 px-2 py-1 text-xs text-slate-300 hover:bg-slate-800"
                >
                  Browse…
                </button>
                <ActionButton
                  label="Save"
                  onClick={() => updatePath.mutate({ projectId: project.id, repositoryPath })}
                  pending={updatePath.isPending}
                />
              </div>
            ) : (
              <div className="text-sm text-slate-300">{project.repository_path}</div>
            )}
          </div>

          <div className="flex flex-col gap-1">
            <label className="text-xs uppercase tracking-wide text-slate-500">Default Branch</label>
            {editable ? (
              <div className="flex items-center gap-2">
                <input
                  value={defaultBranch}
                  onChange={(e) => setDefaultBranch(e.target.value)}
                  className="flex-1 rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-sm text-slate-200"
                />
                <ActionButton
                  label="Save"
                  onClick={() => updateBranch.mutate({ projectId: project.id, defaultBranch })}
                  pending={updateBranch.isPending}
                />
              </div>
            ) : (
              <div className="text-sm text-slate-300">{project.default_branch}</div>
            )}
          </div>
        </div>
      </Panel>
    </div>
  );
}

function Stat({ label, value, tone = "neutral" }: { label: string; value: number; tone?: "warn" | "neutral" }) {
  const color = tone === "warn" && value > 0 ? "text-amber-400" : "text-slate-100";
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900/40 p-3">
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className={`text-lg font-semibold ${color}`}>{value}</div>
    </div>
  );
}

function TasksTab({ project }: { project: ProjectDetailData }) {
  const { data: agents } = useQuery({
    queryKey: ["agents", project.id],
    queryFn: () => api.getAgents(project.id),
  });
  const idleAgents = (agents ?? []).filter((a) => a.status === "idle" && a.role !== "lead");

  return (
    <div className="flex flex-col gap-4">
      <DependencyGraph tasks={allTasks(project)} />

      <Panel title="Tasks">
        <div className="flex flex-col gap-3">
          <div>
            <NewGoalForm projectId={project.id} />
          </div>
          {project.tasks.length === 0 ? (
            <div className="text-sm text-slate-500">
              No tasks yet -- create a goal above, then Plan it to break it down into tasks.
            </div>
          ) : (
            project.tasks.map((node) => <TaskTree key={node.task.id} node={node} idleAgents={idleAgents} />)
          )}
        </div>
      </Panel>
    </div>
  );
}

function DecisionsTab({ project }: { project: ProjectDetailData }) {
  if (project.decisions.length === 0) {
    return (
      <Panel title="Decisions">
        <div className="text-sm text-slate-500">No decisions raised.</div>
      </Panel>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      {project.decisions.map((d) => (
        <DecisionTabCard key={d.id} decision={d} />
      ))}
    </div>
  );
}

function DecisionTabCard({ decision }: { decision: Decision }) {
  const resolve = useAction((response: string) => api.resolveDecision(decision.id, response), {
    successMessage: "Decision resolved",
    invalidateKeys: [["project", decision.project_id], ["decisions"]],
  });

  return (
    <InboxCardShell
      kind="decision"
      title={decision.question}
      createdAt={decision.created_at}
      actions={
        decision.status === "open" ? (
          <>
            <ActionButton label="Approve" onClick={() => resolve.mutate("approved")} pending={resolve.isPending} />
            <ActionButton
              label="Reject"
              tone="danger"
              onClick={() => resolve.mutate("rejected")}
              pending={resolve.isPending}
            />
            <RespondField label="Custom Response" onSubmit={(value) => resolve.mutate(value)} pending={resolve.isPending} />
          </>
        ) : (
          <span className="text-xs text-slate-500">Answer: {decision.answer}</span>
        )
      }
    >
      <p>{decision.context}</p>
      {decision.creation_reason && (
        <p className="mt-1 text-xs text-slate-500">Why this was raised: {decision.creation_reason}</p>
      )}
    </InboxCardShell>
  );
}

function ArtifactsTab({ project }: { project: ProjectDetailData }) {
  const { data: diffs } = useQuery({ queryKey: ["recentDiffs", 200], queryFn: () => api.getRecentDiffs(200) });
  const { data: prompts } = useQuery({ queryKey: ["recentPrompts", 200], queryFn: () => api.getRecentPrompts(200) });

  const projectDiffs = (diffs ?? []).filter((d) => d.project_id === project.id);
  const projectPrompts = (prompts ?? []).filter((p) => p.project_id === project.id);

  return (
    <div className="flex flex-col gap-4">
      <PlanningHistoryPanel projectId={project.id} />

      <Panel title="Recent Diffs">
        {projectDiffs.length === 0 ? (
          <div className="text-sm text-slate-500">No diffs captured yet.</div>
        ) : (
          <div className="flex flex-col gap-3">
            {projectDiffs.map((d) => (
              <div key={d.id} className="rounded-md border border-slate-800 p-2">
                <div className="text-xs text-slate-400">{d.task_title}</div>
                <DiffView content={d.content} className="mt-1 max-h-64 overflow-auto" />
              </div>
            ))}
          </div>
        )}
      </Panel>

      <Panel title="Recent Prompts">
        {projectPrompts.length === 0 ? (
          <div className="text-sm text-slate-500">No prompts generated yet.</div>
        ) : (
          <div className="flex flex-col gap-3">
            {projectPrompts.map((p) => (
              <div key={p.id} className="rounded-md border border-slate-800 p-2">
                <div className="text-xs text-slate-400">{p.task_title}</div>
                <pre className="mt-1 max-h-64 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                  {p.content}
                </pre>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}

const HEALTH_COLOR: Record<AgentHealthStatus, string> = {
  healthy: "text-emerald-400",
  slow: "text-amber-400",
  stalled: "text-rose-400",
};

function AgentsTab({ project }: { project: ProjectDetailData }) {
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);

  const { data: agents } = useQuery({
    queryKey: ["agents", project.id],
    queryFn: () => api.getAgents(project.id),
  });
  const { data: active } = useQuery({ queryKey: ["activeExecution"], queryFn: api.getActiveExecution });
  const { data: health } = useQuery({ queryKey: ["agentHealth"], queryFn: api.getAgentHealth });
  const { data: workspaces } = useQuery({ queryKey: ["workspaces"], queryFn: api.getWorkspaces });
  const { data: agentTemplates } = useQuery({ queryKey: ["agentTemplates"], queryFn: api.getAgentTemplates });

  const selectedAgent = (agents ?? []).find((a) => a.id === selectedAgentId) ?? null;
  const attention = (health ?? []).filter((a) => a.project_id === project.id && a.health !== "healthy");
  const projectActive = (active ?? []).filter((a) => a.project_id === project.id);
  const projectWorkspaces = (workspaces ?? []).filter((w) => w.project_id === project.id);

  return (
    <div className="flex flex-col gap-4">
      <Panel title="Active Executions">
        {projectActive.length === 0 ? (
          <div className="text-sm text-slate-500">No tasks in progress.</div>
        ) : (
          <ul className="flex flex-col gap-2">
            {projectActive.map((item) => (
              <li
                key={item.task_id}
                className="flex items-center justify-between rounded-md border border-slate-800 bg-slate-900/60 px-3 py-2 text-sm"
              >
                <span className="text-slate-200">{item.task_title}</span>
                <span className="text-xs text-slate-500">{item.agent_name}</span>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      {attention.length > 0 && (
        <Panel title="Needs Attention">
          <ul className="flex flex-col gap-2">
            {attention.map((a) => (
              <li key={a.agent_id} className="flex items-center justify-between text-sm">
                <div>
                  <span className="text-slate-200">{a.agent_name}</span>
                  {a.current_task_title && <span className="ml-2 text-xs text-slate-500">{a.current_task_title}</span>}
                </div>
                <div className="flex items-center gap-3 text-xs">
                  <span className="text-slate-500">
                    {a.last_heartbeat ? `heartbeat ${formatRelativeTime(a.last_heartbeat)}` : "no heartbeat"}
                  </span>
                  <span className={`font-semibold uppercase ${HEALTH_COLOR[a.health]}`}>{a.health}</span>
                </div>
              </li>
            ))}
          </ul>
        </Panel>
      )}

      <div className="grid grid-cols-2 gap-4">
        <Panel title="Organization">
          <AgentTree agents={agents ?? []} selectedAgentId={selectedAgentId} onSelect={setSelectedAgentId} />
        </Panel>

        {selectedAgent ? (
          <AgentDetail agent={selectedAgent} />
        ) : (
          <Panel title="Agent Templates">
            {!agentTemplates || agentTemplates.length === 0 ? (
              <div className="text-sm text-slate-500">No agent templates configured.</div>
            ) : (
              <ul className="flex flex-col gap-1.5">
                {agentTemplates.map((t) => (
                  <li key={t.id} className="flex items-center justify-between text-sm">
                    <span className="text-slate-300">{t.label}</span>
                    <span className="text-xs text-slate-500">
                      {t.runtime_profile ? `${t.runtime_profile.runtime_id} / ${t.runtime_profile.model}` : "unresolved"}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
        )}
      </div>

      <Panel title="Workspaces">
        {projectWorkspaces.length === 0 ? (
          <EmptyState message="No active workspaces." />
        ) : (
          <ul className="flex flex-col gap-3">
            {projectWorkspaces.map((w) => (
              <li key={w.task_id} className="rounded-md border border-slate-800 p-3 text-sm">
                <div className="flex items-center justify-between">
                  <span className="font-medium text-slate-100">{w.task_title}</span>
                  <span className="text-xs text-slate-500">{w.agent_name}</span>
                </div>
                <div className="mt-1 break-all text-xs text-slate-400">
                  {w.branch} · {w.git_worktree}
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  <ActionButton label="Open Workspace" onClick={() => revealItemInDir(w.git_worktree)} />
                  <ActionButton
                    label="Copy tmux attach"
                    onClick={() => {
                      navigator.clipboard.writeText(`tmux attach -t ${w.tmux_session}`);
                      pushToast("Copied to clipboard", "success");
                    }}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}
