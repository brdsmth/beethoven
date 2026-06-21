import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import { useAction } from "../lib/hooks";
import { ActionButton } from "../components/ActionButton";
import { Panel } from "../components/Panel";
import { RespondField } from "../components/RespondField";
import type { DecisionSummary } from "../lib/types";

export function DecisionsPage() {
  const { data } = useQuery({ queryKey: ["decisions"], queryFn: api.getDecisions });
  const open = (data ?? []).filter((d) => d.status === "open");
  const resolved = (data ?? []).filter((d) => d.status !== "open");

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Decisions</h1>
      <Panel title={`Open (${open.length})`}>
        {open.length === 0 ? (
          <div className="text-sm text-slate-500">No open decisions.</div>
        ) : (
          <ul className="flex flex-col gap-3">
            {open.map((d) => (
              <DecisionRow key={d.id} decision={d} />
            ))}
          </ul>
        )}
      </Panel>
      <Panel title="Resolved">
        {resolved.length === 0 ? (
          <div className="text-sm text-slate-500">No resolved decisions yet.</div>
        ) : (
          <ul className="flex flex-col gap-2">
            {resolved.map((d) => (
              <li key={d.id} className="flex items-center justify-between text-sm">
                <span className="text-slate-300">{d.question}</span>
                <span className="text-xs text-slate-500">{d.project_slug}</span>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}

function DecisionRow({ decision }: { decision: DecisionSummary }) {
  const invalidateKeys = [["decisions"], ["project", decision.project_id]];
  const resolve = useAction(
    ({ response }: { response: string }) => api.resolveDecision(decision.id, response),
    { successMessage: "Decision resolved", invalidateKeys },
  );

  return (
    <li className="rounded-md border border-slate-800 p-3">
      <div className="flex items-center justify-between">
        <Link to={`/projects/${decision.project_id}`} className="font-medium text-slate-100 hover:underline">
          {decision.question}
        </Link>
        <span className="text-xs text-slate-500">{decision.project_slug}</span>
      </div>
      <p className="mt-1 text-sm text-slate-400">{decision.context}</p>
      {decision.task_title && <div className="mt-1 text-xs text-slate-500">{decision.task_title}</div>}
      {decision.creation_reason && (
        <div className="mt-1 text-xs text-slate-500">Why this was raised: {decision.creation_reason}</div>
      )}
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <ActionButton
          label="Approve"
          onClick={() => resolve.mutate({ response: "approved" })}
          pending={resolve.isPending}
        />
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
