import { Link } from "react-router-dom";
import { useProjects } from "../lib/hooks";
import { EmptyState } from "../components/EmptyState";
import { RuntimeStartNudge } from "../components/RuntimeStartNudge";
import { RuntimeStatusPanel } from "../components/mission-control/RuntimeStatusPanel";
import { PendingDecisionsPanel } from "../components/mission-control/PendingDecisionsPanel";
import { BlockedWorkPanel } from "../components/mission-control/BlockedWorkPanel";
import { ActiveExecution } from "../components/mission-control/ActiveExecution";
import { ActivityFeed } from "../components/mission-control/ActivityFeed";

/// A true cross-project rollup, not a single-selected-project view -- the
/// user runs hundreds of agents across many projects at once, so Home
/// needs to answer "what's blocked, what's waiting on me, what's running"
/// across all of them within five seconds, not just for whichever project
/// happened to be selected last. Runtime Status leads -- nothing executes
/// without the daemon, so its state shouldn't be the last thing you see.
export function HomePage() {
  const { data: projects } = useProjects();

  if (!projects || projects.length === 0) {
    return (
      <EmptyState
        message="No projects yet -- create one to get started."
        actionLabel="New Project"
        actionTo="/projects"
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold text-slate-100">Home</h1>
        <Link to="/artifacts" className="text-sm text-slate-400 hover:text-slate-200">
          View Artifacts →
        </Link>
      </div>

      <RuntimeStartNudge />
      <RuntimeStatusPanel />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <BlockedWorkPanel />
        <PendingDecisionsPanel />
        <ActiveExecution />
      </div>

      <ActivityFeed />
    </div>
  );
}
