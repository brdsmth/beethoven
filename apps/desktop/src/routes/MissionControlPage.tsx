import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useSelectedProject } from "../lib/hooks";
import { ProjectPicker } from "../components/ProjectPicker";
import { GoalHeader } from "../components/mission-control/GoalHeader";
import { RuntimeStatusPanel } from "../components/mission-control/RuntimeStatusPanel";
import { ProjectHealth } from "../components/mission-control/ProjectHealth";
import { ActiveExecution } from "../components/mission-control/ActiveExecution";
import { WorkflowMap } from "../components/mission-control/WorkflowMap";
import { ActivityFeed } from "../components/mission-control/ActivityFeed";

export function MissionControlPage() {
  const { projects, selectedProjectId, setSelectedProjectId } = useSelectedProject();
  const { data: goalHeader } = useQuery({
    queryKey: ["goalHeader", selectedProjectId],
    queryFn: () => api.getGoalHeader(selectedProjectId as string),
    enabled: !!selectedProjectId,
  });

  if (!selectedProjectId) {
    return <div className="text-sm text-slate-500">No projects yet.</div>;
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold text-slate-100">Mission Control</h1>
        <ProjectPicker projects={projects} selectedProjectId={selectedProjectId} onChange={setSelectedProjectId} />
      </div>

      <GoalHeader projectId={selectedProjectId} />

      <div className="grid grid-cols-2 gap-4">
        <RuntimeStatusPanel />
        <ProjectHealth projectId={selectedProjectId} />
      </div>

      <ActiveExecution />

      <WorkflowMap initiativeId={goalHeader?.active_initiative_id ?? null} />

      <ActivityFeed />
    </div>
  );
}
