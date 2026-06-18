import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Panel } from "../Panel";

export function WorkflowMap({ initiativeId }: { initiativeId: string | null }) {
  const { data } = useQuery({
    queryKey: ["workflowMap", initiativeId],
    queryFn: () => api.getWorkflowMap(initiativeId as string),
    enabled: !!initiativeId,
  });

  if (!initiativeId) {
    return (
      <Panel title="Workflow Map">
        <div className="text-sm text-slate-500">No active initiative.</div>
      </Panel>
    );
  }

  const stages: { label: string; count: number | undefined }[] = [
    { label: "Initiative", count: data?.initiative_count },
    { label: "Epic", count: data?.epic_count },
    { label: "Task", count: data?.task_count },
    { label: "Execution", count: data?.execution_count },
    { label: "Review", count: data?.review_count },
    { label: "Complete", count: data?.complete_count },
  ];

  return (
    <Panel title="Workflow Map">
      <div className="flex items-center gap-2 overflow-x-auto">
        {stages.map((stage, idx) => (
          <div key={stage.label} className="flex items-center gap-2">
            <div className="flex flex-col items-center rounded-md border border-slate-800 bg-slate-900/60 px-4 py-3">
              <div className="text-lg font-semibold text-slate-100">{stage.count ?? "—"}</div>
              <div className="text-xs uppercase tracking-wide text-slate-500">{stage.label}</div>
            </div>
            {idx < stages.length - 1 && <span className="text-slate-600">→</span>}
          </div>
        ))}
      </div>
    </Panel>
  );
}
