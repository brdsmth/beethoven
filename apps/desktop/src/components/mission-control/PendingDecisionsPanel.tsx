import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../../lib/api";
import { Panel } from "../Panel";

const CAP = 10;

/// Just a capped list + link to Inbox -- resolving lives in Inbox
/// (DecisionCard), which already shows the full question/context.
/// Repeating that UI here too just gave it two places to drift apart.
export function PendingDecisionsPanel() {
  const { data } = useQuery({ queryKey: ["decisions"], queryFn: api.getDecisions });
  const pending = (data ?? []).filter((d) => d.status === "open");
  const visible = pending.slice(0, CAP);

  return (
    <Panel title="Pending Decisions">
      {pending.length === 0 ? (
        <div className="text-sm text-slate-500">Nothing waiting on you.</div>
      ) : (
        <div className="flex flex-col gap-2">
          <ul className="flex flex-col gap-1.5">
            {visible.map((d) => (
              <li key={d.id}>
                <Link
                  to="/inbox"
                  className="flex items-center justify-between gap-2 text-sm text-slate-300 hover:text-slate-100"
                >
                  <span className="truncate">{d.question}</span>
                  <span className="shrink-0 text-xs text-amber-400">{d.project_slug}</span>
                </Link>
              </li>
            ))}
          </ul>
          {pending.length > CAP && (
            <Link to="/inbox" className="text-xs text-slate-500 hover:text-slate-300">
              view all {pending.length} pending decisions →
            </Link>
          )}
        </div>
      )}
    </Panel>
  );
}
