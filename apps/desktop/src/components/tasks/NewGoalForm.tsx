import { useState } from "react";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import { ActionButton } from "../ActionButton";

/// Creates a root task (no `parentTaskId`) -- a goal, in the old
/// terminology. Free-form prose is fine: the model-driven planner (Phase
/// 2.5A/B) doesn't require "- item" bullet lines the way the legacy stub
/// planner did, though they still help it avoid duplicating breakdown work.
export function NewGoalForm({ projectId, onCreated = () => {} }: { projectId: string; onCreated?: () => void }) {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");

  const create = useAction(
    ({ title, description }: { title: string; description: string }) =>
      api.createTask(projectId, null, title, description, "", null, false),
    {
      successMessage: (vars) => `"${vars.title}" created`,
      invalidateKeys: [["project", projectId], ["tasks", projectId]],
    },
  );

  if (!open) {
    return <ActionButton label="New Goal" onClick={() => setOpen(true)} />;
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border border-slate-800 p-3">
      <input
        autoFocus
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        placeholder="What should this project accomplish?"
        className="w-full rounded-md border border-slate-700 bg-slate-950 px-2 py-1.5 text-sm text-slate-200"
      />
      <textarea
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="Optional details -- the planner will break this down into tasks once created."
        rows={3}
        className="w-full rounded-md border border-slate-700 bg-slate-950 px-2 py-1.5 text-sm text-slate-200"
      />
      <div className="flex justify-end gap-2">
        <button
          onClick={() => setOpen(false)}
          disabled={create.isPending}
          className="rounded-md border border-slate-700 px-3 py-1.5 text-xs text-slate-300 hover:bg-slate-800 disabled:opacity-50"
        >
          Cancel
        </button>
        <button
          onClick={() =>
            create.mutate(
              { title: title.trim(), description: description.trim() },
              {
                onSuccess: () => {
                  setOpen(false);
                  setTitle("");
                  setDescription("");
                  onCreated();
                },
              },
            )
          }
          disabled={title.trim().length === 0 || create.isPending}
          className="rounded-md bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {create.isPending ? "Creating…" : "Create Goal"}
        </button>
      </div>
    </div>
  );
}
