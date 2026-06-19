import { TaskDetailPanel } from "./TaskDetailPanel";

export function TaskDetailModal({ taskId, onClose }: { taskId: string; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6">
      <div className="max-h-full w-full max-w-2xl overflow-y-auto rounded-lg border border-slate-700 bg-slate-900 p-5 shadow-xl">
        <div className="mb-3 flex justify-end">
          <button onClick={onClose} className="text-xs text-slate-500 hover:text-slate-300">
            Close ✕
          </button>
        </div>
        <TaskDetailPanel taskId={taskId} />
      </div>
    </div>
  );
}
