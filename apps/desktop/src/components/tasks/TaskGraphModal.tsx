import { TaskGraphView } from "./TaskGraphView";

/// A separate, wider shell from TaskDetailModal -- the graph needs room for
/// several node columns side by side. TaskDetailModal stays untouched
/// since ArtifactsPage still opens it for its own "View Task" action.
export function TaskGraphModal({
  taskId,
  taskTitle,
  onClose,
}: {
  taskId: string;
  taskTitle: string;
  onClose: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6">
      <div className="max-h-full w-full max-w-5xl overflow-y-auto rounded-lg border border-slate-700 bg-slate-900 p-5 shadow-xl">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-100">{taskTitle} — execution graph</h2>
          <button onClick={onClose} className="text-xs text-slate-500 hover:text-slate-300">
            Close ✕
          </button>
        </div>
        <TaskGraphView taskId={taskId} />
      </div>
    </div>
  );
}
