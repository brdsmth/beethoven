import type { Task } from "../../lib/types";
import { Panel } from "../Panel";
import { displayStatus } from "./taskDisplay";

/**
 * A visual rendering of the dependency edges between a project's tasks --
 * not the parent/child tree (that's TaskGraph) -- laid out top to bottom by
 * dependency depth, e.g. Backend -> Tests -> Review. Purpose, per the spec:
 * help users understand scheduler behavior, not be a general-purpose graph
 * renderer, so this stays plain divs/arrows rather than pulling in a graph
 * layout library for a handful of edges.
 */
export function DependencyGraph({ tasks }: { tasks: Task[] }) {
  const involved = tasks.filter((t) => t.dependencies.length > 0 || t.dependents.length > 0);

  if (involved.length === 0) {
    return (
      <Panel title="Dependency Graph">
        <div className="text-sm text-slate-500">No task dependencies recorded yet.</div>
      </Panel>
    );
  }

  const byId = new Map(tasks.map((t) => [t.id, t]));
  const levelOf = new Map<string, number>();
  function levelFor(taskId: string, seen: Set<string>): number {
    if (levelOf.has(taskId)) return levelOf.get(taskId)!;
    if (seen.has(taskId)) return 0; // cycle guard -- the backend rejects cycles, but never hang the UI on bad data
    seen.add(taskId);
    const deps = byId.get(taskId)?.dependencies ?? [];
    const lvl = deps.length === 0 ? 0 : 1 + Math.max(...deps.map((d) => levelFor(d.id, seen)));
    levelOf.set(taskId, lvl);
    return lvl;
  }

  const rows = new Map<number, Task[]>();
  for (const t of involved) {
    const lvl = levelFor(t.id, new Set());
    const row = rows.get(lvl) ?? [];
    row.push(t);
    rows.set(lvl, row);
  }
  const sortedLevels = Array.from(rows.keys()).sort((a, b) => a - b);

  return (
    <Panel title="Dependency Graph">
      <div className="flex flex-col items-center gap-2">
        {sortedLevels.map((lvl, idx) => (
          <div key={lvl} className="flex flex-col items-center gap-2">
            <div className="flex flex-wrap items-center justify-center gap-3">
              {rows.get(lvl)!.map((t) => {
                const { label, color } = displayStatus(t);
                return (
                  <div
                    key={t.id}
                    className="flex flex-col items-center rounded-md border border-slate-800 bg-slate-900/60 px-3 py-2"
                  >
                    <span className="text-sm font-medium text-slate-200">{t.title}</span>
                    <span className={`text-xs font-semibold uppercase ${color}`}>{label}</span>
                  </div>
                );
              })}
            </div>
            {idx < sortedLevels.length - 1 && <span className="text-slate-600">↓</span>}
          </div>
        ))}
      </div>
    </Panel>
  );
}
