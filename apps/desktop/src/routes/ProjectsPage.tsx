import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useProjects } from "../lib/hooks";
import { ProjectList } from "../components/projects/ProjectList";
import { NewProjectDialog } from "../components/projects/NewProjectDialog";
import { ActionButton } from "../components/ActionButton";

export function ProjectsPage() {
  const { data } = useProjects();
  const [creating, setCreating] = useState(false);
  const navigate = useNavigate();

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold text-slate-100">Projects</h1>
        <ActionButton label="New Project" onClick={() => setCreating(true)} />
      </div>
      <ProjectList projects={data ?? []} />

      {creating && (
        <NewProjectDialog
          onClose={() => setCreating(false)}
          onCreated={(project) => {
            setCreating(false);
            navigate(`/projects/${project.id}`);
          }}
        />
      )}
    </div>
  );
}
