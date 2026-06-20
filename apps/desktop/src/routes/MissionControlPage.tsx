import { useSelectedProject } from "../lib/hooks";
import { GoalHeader } from "../components/mission-control/GoalHeader";
import { RuntimeStatusPanel } from "../components/mission-control/RuntimeStatusPanel";
import { ProjectsOverviewPanel } from "../components/mission-control/ProjectsOverviewPanel";
import { PendingDecisionsPanel } from "../components/mission-control/PendingDecisionsPanel";
import { QueueHealthPanel } from "../components/mission-control/QueueHealthPanel";
import { BlockedWorkPanel } from "../components/mission-control/BlockedWorkPanel";
import { ActiveExecution } from "../components/mission-control/ActiveExecution";
import { ActivityFeed } from "../components/mission-control/ActivityFeed";

/// What is being built, what is blocked, what needs attention, what is the
/// current bottleneck, is work progressing -- answerable within five
/// seconds, per the scheduler-centric UX spec. Three rows: outcomes
/// (Projects/Progress/Decisions), work state (Running/Blocked/Queue), then
/// the audit trail (Recent Changes/Runtime Status).
export function MissionControlPage() {
  const { projects, selectedProjectId, setSelectedProjectId } = useSelectedProject();

  if (!selectedProjectId) {
    return <div className="text-sm text-slate-500">No projects yet.</div>;
  }

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Mission Control</h1>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <ProjectsOverviewPanel selectedProjectId={selectedProjectId} onSelect={setSelectedProjectId} />
        <GoalHeader projectId={selectedProjectId} />
        <PendingDecisionsPanel projectId={selectedProjectId} />
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <ActiveExecution projectId={selectedProjectId} />
        <BlockedWorkPanel projectId={selectedProjectId} />
        <QueueHealthPanel projectId={selectedProjectId} />
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <ActivityFeed />
        <RuntimeStatusPanel />
      </div>

      {projects.length === 0 && <div className="text-sm text-slate-500">No projects yet.</div>}
    </div>
  );
}
