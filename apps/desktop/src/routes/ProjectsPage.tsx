import { useProjects } from "../lib/hooks";
import { ProjectList } from "../components/projects/ProjectList";

export function ProjectsPage() {
  const { data } = useProjects();

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Projects</h1>
      <ProjectList projects={data ?? []} />
    </div>
  );
}
