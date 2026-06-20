import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { formatRelativeTime, humanizeEventType } from "../../lib/format";
import { Panel } from "../Panel";

export function ActivityFeed() {
  const { data } = useQuery({ queryKey: ["timeline", "recent"], queryFn: () => api.getTimeline() });
  const events = (data ?? []).slice(0, 15);

  return (
    <Panel title="Recent Changes">
      {events.length === 0 ? (
        <div className="text-sm text-slate-500">No events yet.</div>
      ) : (
        <ul className="flex flex-col gap-1.5">
          {events.map((e) => (
            <li key={e.id} className="flex items-center justify-between text-sm">
              <span className="text-slate-200">{humanizeEventType(e.event_type)}</span>
              <span className="text-xs text-slate-500">{formatRelativeTime(e.timestamp)}</span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
