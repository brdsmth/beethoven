import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { ProjectSummary } from "../../lib/types";
import { ActionButton } from "../ActionButton";
import { ConfirmDialog } from "../ConfirmDialog";

const STATUS_COLOR: Record<ProjectSummary["status"], string> = {
  active: "text-emerald-400",
  paused: "text-amber-400",
  archived: "text-slate-500",
};

export function ProjectList({ projects }: { projects: ProjectSummary[] }) {
  if (projects.length === 0) {
    return <div className="text-sm text-slate-500">No projects yet.</div>;
  }

  return (
    <ul className="flex flex-col gap-2">
      {projects.map((p) => (
        <ProjectRow key={p.id} project={p} />
      ))}
    </ul>
  );
}

function ProjectRow({ project }: { project: ProjectSummary }) {
  const [confirmArchive, setConfirmArchive] = useState(false);

  const invalidateKeys = [["projects"], ["project", project.id]];

  const pause = useAction(api.pauseProject, {
    successMessage: `${project.name} paused`,
    invalidateKeys,
  });
  const resume = useAction(api.resumeProject, {
    successMessage: `${project.name} resumed`,
    invalidateKeys,
  });
  const archive = useAction(api.archiveProject, {
    successMessage: `${project.name} archived`,
    invalidateKeys,
  });

  return (
    <li className="flex items-center justify-between rounded-lg border border-slate-800 bg-slate-900/40 p-4 hover:border-indigo-600">
      <Link to={`/projects/${project.id}`} className="flex flex-1 items-center justify-between">
        <div>
          <div className="font-medium text-slate-100">{project.name}</div>
          <div className="text-xs text-slate-500">{project.slug}</div>
        </div>
        <div className="flex items-center gap-4 text-sm text-slate-400">
          <span>{project.task_counts.in_progress} in progress</span>
          <span>{project.open_inbox_count} inbox</span>
          <span className={STATUS_COLOR[project.status]}>{project.status}</span>
        </div>
      </Link>

      <div className="ml-4 flex items-center gap-2">
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
    </li>
  );
}
