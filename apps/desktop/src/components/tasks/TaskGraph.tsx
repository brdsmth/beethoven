import type { TaskNode } from "../../lib/types";
import { displayStatus } from "./taskDisplay";

/**
 * The task tree, scheduler-centric: every node shows what the doc's
 * "Task Graph View" asks for -- a status word (Complete/Running/Waiting/
 * Improving/Blocked), and for leaves, attempt/target plus which unmet
 * dependency is actually holding it back, not just "Ready".
 */
export function TaskGraph({ nodes, onSelectTask }: { nodes: TaskNode[]; onSelectTask?: (taskId: string) => void }) {
  if (nodes.length === 0) {
    return <div className="text-sm text-slate-500">No tasks yet.</div>;
  }

  return (
    <div className="flex flex-col gap-2">
      {nodes.map((node) => (
        <TaskGraphNode key={node.task.id} node={node} onSelectTask={onSelectTask} />
      ))}
    </div>
  );
}

function TaskGraphNode({ node, onSelectTask }: { node: TaskNode; onSelectTask?: (taskId: string) => void }) {
  const { task, children } = node;
  const { label, color } = displayStatus(task);
  const blockers = task.dependencies.filter((d) => d.status !== "done");

  return (
    <div className="flex flex-col gap-1.5">
      <button
        type="button"
        onClick={() => onSelectTask?.(task.id)}
        disabled={!onSelectTask}
        className="flex w-full items-center justify-between gap-3 rounded-md border border-slate-800 bg-slate-900/40 px-3 py-2 text-left enabled:hover:bg-slate-900/70"
      >
        <div className="flex flex-col">
          <span className="text-sm font-medium text-slate-200">{task.title}</span>
          {blockers.length > 0 && (
            <span className="text-xs text-amber-400">Blocked by: {blockers.map((b) => b.title).join(", ")}</span>
          )}
        </div>
        <div className="flex items-center gap-3 text-xs text-slate-500">
          {children.length === 0 && (
            <span>
              Attempt {task.attempt} · Target {task.target_score}
            </span>
          )}
          <span className={`font-semibold uppercase ${color}`}>{label}</span>
        </div>
      </button>
      {children.length > 0 && (
        <div className="ml-1.5 flex flex-col gap-1.5 border-l border-slate-800 pl-3">
          {children.map((child) => (
            <TaskGraphNode key={child.task.id} node={child} onSelectTask={onSelectTask} />
          ))}
        </div>
      )}
    </div>
  );
}
