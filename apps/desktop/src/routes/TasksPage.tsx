import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useSelectedProject } from "../lib/hooks";
import { allTasks } from "../lib/projectHealth";
import { ProjectPicker } from "../components/ProjectPicker";
import { Panel } from "../components/Panel";
import { TaskGraph } from "../components/tasks/TaskGraph";
import { DependencyGraph } from "../components/tasks/DependencyGraph";
import { TaskDetailModal } from "../components/TaskDetailModal";

/**
 * Work, not agents: the primary cross-project entry point onto one
 * project's task graph and the dependencies that govern what the
 * scheduler will release next.
 */
export function TasksPage() {
  const { projects, selectedProjectId, setSelectedProjectId } = useSelectedProject();
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);

  const { data: project } = useQuery({
    queryKey: ["project", selectedProjectId],
    queryFn: () => api.getProject(selectedProjectId as string),
    enabled: !!selectedProjectId,
  });

  if (!selectedProjectId) {
    return <div className="text-sm text-slate-500">No projects yet.</div>;
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold text-slate-100">Tasks</h1>
        <ProjectPicker projects={projects} selectedProjectId={selectedProjectId} onChange={setSelectedProjectId} />
      </div>

      <Panel title="Task Graph">
        {project ? (
          <TaskGraph nodes={project.tasks} onSelectTask={setSelectedTaskId} />
        ) : (
          <div className="text-sm text-slate-500">Loading…</div>
        )}
      </Panel>

      {project && <DependencyGraph tasks={allTasks(project)} />}

      {selectedTaskId && <TaskDetailModal taskId={selectedTaskId} onClose={() => setSelectedTaskId(null)} />}
    </div>
  );
}
