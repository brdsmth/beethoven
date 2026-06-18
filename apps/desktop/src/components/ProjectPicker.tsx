import type { ProjectSummary } from "../lib/types";

export function ProjectPicker({
  projects,
  selectedProjectId,
  onChange,
}: {
  projects: ProjectSummary[];
  selectedProjectId: string | null;
  onChange: (id: string) => void;
}) {
  return (
    <select
      value={selectedProjectId ?? ""}
      onChange={(e) => onChange(e.target.value)}
      className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200"
    >
      {projects.length === 0 && <option value="">No projects</option>}
      {projects.map((p) => (
        <option key={p.id} value={p.id}>
          {p.name}
        </option>
      ))}
    </select>
  );
}
