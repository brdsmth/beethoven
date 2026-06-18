import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useProjects } from "../../lib/hooks";
import { useViewStore } from "../../lib/store";
import { humanizeEventType } from "../../lib/format";

function startOfDayUtc(date: string): string {
  return `${date}T00:00:00Z`;
}

function endOfDayUtc(date: string): string {
  return `${date}T23:59:59Z`;
}

export function EventStream() {
  const { data: projects } = useProjects();
  const filters = useViewStore((s) => s.timelineFilters);
  const setFilters = useViewStore((s) => s.setTimelineFilters);

  const { data: agents } = useQuery({
    queryKey: ["agents", filters.projectId],
    queryFn: () => api.getAgents(filters.projectId),
    enabled: !!filters.projectId,
  });

  const { data: events } = useQuery({
    queryKey: ["timeline", filters],
    queryFn: () =>
      api.getTimeline({
        projectId: filters.projectId || undefined,
        agentId: filters.agentId || undefined,
        eventType: filters.eventType || undefined,
        since: filters.since ? startOfDayUtc(filters.since) : undefined,
        until: filters.until ? endOfDayUtc(filters.until) : undefined,
      }),
  });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-3">
        <select
          value={filters.projectId}
          onChange={(e) => setFilters({ projectId: e.target.value, agentId: "" })}
          className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200"
        >
          <option value="">All projects</option>
          {(projects ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>

        <select
          value={filters.agentId}
          onChange={(e) => setFilters({ agentId: e.target.value })}
          disabled={!filters.projectId}
          className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200 disabled:opacity-50"
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
          value={filters.eventType}
          onChange={(e) => setFilters({ eventType: e.target.value })}
          className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200"
        />

        <input
          type="date"
          value={filters.since}
          onChange={(e) => setFilters({ since: e.target.value })}
          className="rounded-md border border-slate-700 bg-slate-900 px-3 py-1.5 text-sm text-slate-200"
        />
        <input
          type="date"
          value={filters.until}
          onChange={(e) => setFilters({ until: e.target.value })}
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
