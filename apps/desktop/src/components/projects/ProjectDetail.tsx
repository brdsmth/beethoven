import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { pickDirectory } from "../../lib/dialog";
import { useAction } from "../../lib/hooks";
import { allTasks, computeProjectHealth, currentBottleneck } from "../../lib/projectHealth";
import { displayStatus } from "../tasks/taskDisplay";
import { DependencyGraph } from "../tasks/DependencyGraph";
import { NewGoalForm } from "../tasks/NewGoalForm";
import { PlanningSummaryPanel } from "../tasks/PlanningSummaryPanel";
import { PlanningHistoryPanel } from "../tasks/PlanningHistoryPanel";
import type { Agent, ProjectDetail as ProjectDetailData, Task, TaskNode } from "../../lib/types";
import { ActionButton } from "../ActionButton";
import { ConfirmDialog } from "../ConfirmDialog";
import { Panel } from "../Panel";
import { RespondField } from "../RespondField";
import { Tabs } from "../Tabs";
import { TaskDetailModal } from "../TaskDetailModal";
import { GoalHeader } from "../mission-control/GoalHeader";

const STATUS_COLOR: Record<ProjectDetailData["status"], string> = {
  active: "text-emerald-400",
  paused: "text-amber-400",
  archived: "text-slate-500",
};

type ProjectTab = "overview" | "task_graph" | "decisions" | "artifacts" | "runtime";
const TABS: { id: ProjectTab; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "task_graph", label: "Task Graph" },
  { id: "decisions", label: "Decisions" },
  { id: "artifacts", label: "Artifacts" },
  { id: "runtime", label: "Runtime" },
];

export function ProjectDetail({ project }: { project: ProjectDetailData }) {
  const [confirmArchive, setConfirmArchive] = useState(false);
  const [tab, setTab] = useState<ProjectTab>("overview");

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
          <span className={STATUS_COLOR[project.status]}>{project.status}</span>
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
      {tab === "task_graph" && <TaskGraphTab project={project} />}
      {tab === "decisions" && <DecisionsTab project={project} />}
      {tab === "artifacts" && <ArtifactsTab project={project} />}
      {tab === "runtime" && <RuntimeTab project={project} />}

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
    </div>
  );
}

/// Tasks only ever get worked on while the global daemon is ticking --
/// without it, a freshly created and planned project just sits there with
/// Ready tasks no one picks up. Surfaced here (not just on the Runtime
/// page) since this is exactly the moment a new project needs it.
function RuntimeStartNudge() {
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

function Stat({ label, value, tone = "neutral" }: { label: string; value: number; tone?: "warn" | "neutral" }) {
  const color = tone === "warn" && value > 0 ? "text-amber-400" : "text-slate-100";
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900/40 p-3">
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className={`text-lg font-semibold ${color}`}>{value}</div>
    </div>
  );
}

function TaskGraphTab({ project }: { project: ProjectDetailData }) {
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
  return (
    <Panel title="Decisions">
      {project.decisions.length === 0 ? (
        <div className="text-sm text-slate-500">No decisions raised.</div>
      ) : (
        <ul className="flex flex-col gap-1">
          {project.decisions.map((d) => (
            <li key={d.id} className="text-sm text-slate-300">
              {d.question}{" "}
              <span className={`text-xs ${d.status === "open" ? "text-amber-400" : "text-slate-500"}`}>
                ({d.status})
              </span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
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
                <pre className="mt-1 max-h-64 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                  {d.content}
                </pre>
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

function RuntimeTab({ project }: { project: ProjectDetailData }) {
  const { data: active } = useQuery({ queryKey: ["activeExecution"], queryFn: api.getActiveExecution });
  const projectActive = (active ?? []).filter((a) => a.project_id === project.id);

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

      <ProjectSettings project={project} />
    </div>
  );
}

function ProjectSettings({ project }: { project: ProjectDetailData }) {
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

  const { data: agentTemplates } = useQuery({ queryKey: ["agentTemplates"], queryFn: api.getAgentTemplates });

  return (
    <Panel title="Settings">
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

        <div>
          <div className="text-xs uppercase tracking-wide text-slate-500">Runtime</div>
          {!agentTemplates || agentTemplates.length === 0 ? (
            <div className="mt-1 text-sm text-slate-500">No agent templates configured.</div>
          ) : (
            <ul className="mt-2 flex flex-col gap-1.5">
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
        </div>
      </div>
    </Panel>
  );
}

/// A task plus its descendant tree -- containers (tasks with children) just
/// render a header and recurse; leaves render the full `TaskRow` actions,
/// since only leaves are ever delegated to an agent.
function TaskTree({ node, idleAgents }: { node: TaskNode; idleAgents: Agent[] }) {
  if (node.children.length === 0) {
    return <TaskRow task={node.task} idleAgents={idleAgents} />;
  }

  const { label, color } = displayStatus(node.task);

  return (
    <div className="rounded-md border border-slate-800 p-3">
      <div className="flex items-center justify-between">
        <div className="font-medium text-slate-100">{node.task.title}</div>
        <span className={`text-xs font-semibold uppercase ${color}`}>{label}</span>
      </div>
      {node.task.description && <p className="mt-1 text-sm text-slate-400">{node.task.description}</p>}
      <div className="mt-3 flex flex-col gap-2 pl-3">
        {node.children.map((child) => (
          <TaskTree key={child.task.id} node={child} idleAgents={idleAgents} />
        ))}
      </div>
    </div>
  );
}

function TaskRow({ task, idleAgents }: { task: Task; idleAgents: Agent[] }) {
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [reassignOpen, setReassignOpen] = useState(false);
  const [viewDetail, setViewDetail] = useState(false);

  const invalidateKeys = [["project", task.project_id], ["tasks", task.project_id]];
  const plan = useAction(api.planTask, {
    successMessage: (_taskId, outcome) =>
      `Planned ${outcome.tasks.length} task(s), ${outcome.decisions.length} decision(s)`,
    invalidateKeys,
  });
  const retry = useAction(api.retryTask, { successMessage: "Task retried", invalidateKeys });
  const cancel = useAction(api.cancelTask, { successMessage: "Task cancelled", invalidateKeys });
  const reassign = useAction(
    ({ taskId, newAgentId }: { taskId: string; newAgentId: string }) => api.reassignTask(taskId, newAgentId),
    { successMessage: "Task reassigned", invalidateKeys: [...invalidateKeys, ["agents", task.project_id]] },
  );
  const approve = useAction(api.approveReview, { successMessage: "Review approved", invalidateKeys });
  const requestChanges = useAction(
    ({ taskId, reason }: { taskId: string; reason: string }) => api.requestReviewChanges(taskId, reason),
    { successMessage: "Changes requested", invalidateKeys },
  );

  const canCancel = task.status !== "done" && task.status !== "cancelled";
  const canReassign = (task.status === "assigned" || task.status === "blocked") && idleAgents.length > 0;
  const blockers = task.dependencies.filter((d) => d.status !== "done");
  const { label, color } = displayStatus(task);

  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-slate-800 px-3 py-2">
      <div className="flex items-center justify-between text-sm">
        <span className="text-slate-300">{task.title}</span>
        <span className={`text-xs font-semibold uppercase ${color}`}>{label}</span>
      </div>
      {blockers.length > 0 && (
        <div className="text-xs text-amber-400">
          Blocked by:{" "}
          {blockers.map((b, idx) => (
            <span key={b.id} title={b.dependency_reason ?? undefined}>
              {idx > 0 && ", "}
              {b.title}
            </span>
          ))}
        </div>
      )}
      {task.status === "blocked" && task.blocked_reason && (
        <div className="text-xs text-rose-400">{task.blocked_reason}</div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {task.status === "ready" && (
          <ActionButton
            label="Plan"
            title="Break this down into tasks (and, for a model-driven planner, success criteria)."
            onClick={() => plan.mutate(task.id)}
            pending={plan.isPending}
          />
        )}
        <ActionButton label="View Diff" onClick={() => setViewDetail(true)} />
        {task.status === "blocked" && (
          <ActionButton label="Retry" onClick={() => retry.mutate(task.id)} pending={retry.isPending} />
        )}
        {canCancel && <ActionButton label="Cancel" tone="danger" onClick={() => setConfirmCancel(true)} />}
        {canReassign && (
          <ActionButton label="Reassign" onClick={() => setReassignOpen((open) => !open)} />
        )}
        {task.status === "blocked" && (
          <>
            <ActionButton label="Approve" onClick={() => approve.mutate(task.id)} pending={approve.isPending} />
            <RespondField
              label="Request Changes"
              onSubmit={(reason) => requestChanges.mutate({ taskId: task.id, reason })}
              pending={requestChanges.isPending}
            />
          </>
        )}
      </div>

      {reassignOpen && (
        <AgentPicker
          agents={idleAgents}
          pending={reassign.isPending}
          onSelect={(agentId) => {
            reassign.mutate({ taskId: task.id, newAgentId: agentId }, { onSuccess: () => setReassignOpen(false) });
          }}
        />
      )}

      {viewDetail && <TaskDetailModal taskId={task.id} onClose={() => setViewDetail(false)} />}

      <ConfirmDialog
        open={confirmCancel}
        title={`Cancel "${task.title}"?`}
        description="Stops further execution. Work already mid-attempt will not be resurrected to Done when it completes."
        confirmLabel="Cancel Task"
        danger
        pending={cancel.isPending}
        onConfirm={() => cancel.mutate(task.id, { onSuccess: () => setConfirmCancel(false) })}
        onCancel={() => setConfirmCancel(false)}
      />
    </div>
  );
}

function AgentPicker({
  agents,
  onSelect,
  pending,
}: {
  agents: Agent[];
  onSelect: (agentId: string) => void;
  pending?: boolean;
}) {
  const [agentId, setAgentId] = useState(agents[0]?.id ?? "");

  return (
    <div className="flex items-center gap-2">
      <select
        value={agentId}
        onChange={(e) => setAgentId(e.target.value)}
        className="rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200"
      >
        {agents.map((a) => (
          <option key={a.id} value={a.id}>
            {a.name}
          </option>
        ))}
      </select>
      <ActionButton label="Confirm Reassign" onClick={() => agentId && onSelect(agentId)} pending={pending} />
    </div>
  );
}
