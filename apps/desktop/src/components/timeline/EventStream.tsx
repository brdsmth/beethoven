import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { humanizeEventType } from "../../lib/format";

function startOfDayUtc(date: string): string {
  return `${date}T00:00:00Z`;
}

function endOfDayUtc(date: string): string {
  return `${date}T23:59:59Z`;
}

/// One project's full event log, filterable by agent/event-type/date --
/// always scoped to `projectId` now (the project picker this used to have
/// is gone now that this only ever lives inside that project's own Logs
/// tab). Filters are local component state, not global store state:
/// since this mounts fresh per project, a filter set while looking at one
/// project's log shouldn't leak into another's.
export function EventStream({ projectId }: { projectId: string }) {
  const [agentId, setAgentId] = useState("");
  const [eventType, setEventType] = useState("");
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");

  const { data: agents } = useQuery({ queryKey: ["agents", projectId], queryFn: () => api.getAgents(projectId) });

  const { data: events } = useQuery({
    queryKey: ["timeline", projectId, agentId, eventType, since, until],
    queryFn: () =>
      api.getTimeline({
        projectId,
        agentId: agentId || undefined,
        eventType: eventType || undefined,
        since: since ? startOfDayUtc(since) : undefined,
        until: until ? endOfDayUtc(until) : undefined,
      }),
  });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-3">
        <select
          value={agentId}
          onChange={(e) => setAgentId(e.target.value)}
          className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200"
        >
          <option value="">All agents</option>
          {(agents ?? []).map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>

        <input
          type="text"
          placeholder="Event type (e.g. TaskAssigned)"
          value={eventType}
          onChange={(e) => setEventType(e.target.value)}
          className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200"
        />

        <input
          type="date"
          value={since}
          onChange={(e) => setSince(e.target.value)}
          className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200"
        />
        <input
          type="date"
          value={until}
          onChange={(e) => setUntil(e.target.value)}
          className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200"
        />
      </div>

      <ul className="flex flex-col gap-1">
        {(events ?? []).map((e) => (
          <li key={e.id} className="rounded-md border border-slate-800 bg-slate-900/40 px-3 py-2 text-sm">
            <details>
              <summary className="flex cursor-pointer items-center justify-between text-slate-200">
                <span>{humanizeEventType(e.event_type)}</span>
                <span className="text-xs text-slate-500">{new Date(e.timestamp).toLocaleString()}</span>
              </summary>
              <pre className="mt-2 overflow-x-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                {JSON.stringify(e.payload, null, 2)}
              </pre>
            </details>
          </li>
        ))}
        {events && events.length === 0 && <div className="text-sm text-slate-500">No events match these filters.</div>}
      </ul>
    </div>
  );
}
