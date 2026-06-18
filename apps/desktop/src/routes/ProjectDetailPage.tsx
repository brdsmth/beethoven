import { useParams, Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { ProjectDetail } from "../components/projects/ProjectDetail";

export function ProjectDetailPage() {
  const { projectId } = useParams<{ projectId: string }>();
  const { data, isLoading } = useQuery({
    queryKey: ["project", projectId],
    queryFn: () => api.getProject(projectId as string),
    enabled: !!projectId,
  });

  return (
    <div className="flex flex-col gap-4">
      <Link to="/projects" className="text-sm text-indigo-400 hover:underline">
        ← Back to Projects
      </Link>
      {isLoading && <div className="text-sm text-slate-500">Loading…</div>}
      {data && <ProjectDetail project={data} />}
    </div>
  );
}
