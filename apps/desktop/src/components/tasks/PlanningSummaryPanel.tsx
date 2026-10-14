import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { formatRelativeTime } from "../../lib/format";
import { Panel } from "../Panel";

/// "What was planned, why it was planned, how it was planned" -- the most
/// recent planning run for this project. The headline row (tasks, criteria
/// coverage, when) is the default view; the full 11-stat grid, score
/// distribution, and raw summary text are one click away behind "view full
/// breakdown" rather than always on screen. Read-only; these numbers never
/// influence scheduling, only what's shown.
export function PlanningSummaryPanel({ projectId }: { projectId: string }) {
  const [expanded, setExpanded] = useState(false);
  const { data } = useQuery({
    queryKey: ["planningHistory", projectId],
    queryFn: () => api.getPlanningHistory(projectId),
  });
  const latest = data && data.length > 0 ? data[data.length - 1] : null;

  return (
    <Panel title="Latest Planning Run">
      {!latest ? (
        <div className="text-sm text-slate-500">No planning runs yet.</div>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-3">
            <div className="flex flex-col overflow-hidden">
              <span className="truncate text-sm text-slate-200">
                Planning Run #{latest.sequence} -- {latest.planned_task_title}
              </span>
              <span className="truncate text-xs text-slate-500">
                {latest.task_count} tasks · {Math.round(latest.criteria_coverage_percent)}% criteria coverage ·{" "}
                {formatRelativeTime(latest.started_at)}
              </span>
            </div>
            <button
              onClick={() => setExpanded((e) => !e)}
              className="flex-none text-xs text-slate-500 hover:text-slate-300"
            >
              {expanded ? "hide full breakdown" : "view full breakdown"}
            </button>
          </div>

          {expanded && (
            <div className="flex flex-col gap-3 border-t border-slate-800 pt-3">
              <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
                <Stat label="Tasks" value={latest.task_count} />
                <Stat label="Leaf Tasks" value={latest.leaf_task_count} />
                <Stat label="Avg Task Length" value={`${Math.round(latest.average_task_length)} chars`} />
                <Stat label="Dependencies" value={latest.dependency_count} />
                <Stat label="Decisions" value={latest.decision_count} />
                <Stat label="Duration" value={`${latest.planning_duration_ms}ms`} />
                <Stat label="Tasks With Criteria" value={`${latest.tasks_with_criteria}/${latest.task_count}`} />
                <Stat label="Avg Criteria/Task" value={latest.average_criteria_per_task.toFixed(1)} />
                <Stat label="Avg Target Score" value={latest.average_target_score.toFixed(1)} />
                <Stat label="Highest Target Score" value={latest.highest_target_score} />
                <Stat label="Lowest Target Score" value={latest.lowest_target_score} />
              </div>

              <ScoreDistribution bands={latest.score_distribution} />

              <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded bg-slate-950 p-2 text-xs text-slate-400">
                {latest.summary}
              </pre>
            </div>
          )}
        </div>
      )}
    </Panel>
  );
}

function ScoreDistribution({ bands }: { bands: { band: string; count: number }[] }) {
  const total = bands.reduce((sum, b) => sum + b.count, 0);
  if (total === 0) return null;

  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-slate-500">Score Distribution</div>
      <div className="mt-1 flex flex-col gap-1">
        {bands.map((b) => (
          <div key={b.band} className="flex items-center gap-2 text-xs text-slate-400">
            <span className="w-16 shrink-0">{b.band}</span>
            <div className="h-2 flex-1 rounded bg-slate-800">
              <div
                className="h-2 rounded bg-indigo-600"
                style={{ width: total === 0 ? "0%" : `${(b.count / total) * 100}%` }}
              />
            </div>
            <span className="w-6 shrink-0 text-right">{b.count}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className="text-lg font-semibold text-slate-100">{value}</div>
    </div>
  );
}
