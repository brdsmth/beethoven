import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import { ActionButton } from "../ActionButton";
import { Panel } from "../Panel";
import { RespondField } from "../RespondField";
import type { DecisionSummary } from "../../lib/types";

export function PendingDecisionsPanel({ projectId }: { projectId: string }) {
  const { data } = useQuery({ queryKey: ["decisions"], queryFn: api.getDecisions });
  const pending = (data ?? []).filter((d) => d.project_id === projectId && d.status === "open");

  return (
    <Panel title="Pending Decisions">
      {pending.length === 0 ? (
        <div className="text-sm text-slate-500">Nothing waiting on you.</div>
      ) : (
        <ul className="flex flex-col gap-3">
          {pending.map((d) => (
            <DecisionRow key={d.id} decision={d} />
          ))}
        </ul>
      )}
    </Panel>
  );
}

function DecisionRow({ decision }: { decision: DecisionSummary }) {
  const invalidateKeys = [["decisions"], ["project", decision.project_id]];
  const resolve = useAction(({ response }: { response: string }) => api.resolveDecision(decision.id, response), {
    successMessage: "Decision resolved",
    invalidateKeys,
  });

  return (
    <li className="rounded-md border border-slate-800 p-2">
      <div className="text-sm font-medium text-slate-200">{decision.question}</div>
      {decision.task_title && <div className="mt-0.5 text-xs text-slate-500">{decision.task_title}</div>}
      {decision.creation_reason && (
        <div className="mt-0.5 text-xs text-slate-500">Why this was raised: {decision.creation_reason}</div>
      )}
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <ActionButton label="Approve" onClick={() => resolve.mutate({ response: "approved" })} pending={resolve.isPending} />
        <ActionButton
          label="Reject"
          tone="danger"
          onClick={() => resolve.mutate({ response: "rejected" })}
          pending={resolve.isPending}
        />
        <RespondField
          label="Custom Response"
          onSubmit={(response) => resolve.mutate({ response })}
          pending={resolve.isPending}
        />
      </div>
    </li>
  );
}
