import { useState, type ReactNode } from "react";
import { api } from "../../lib/api";
import { pickDirectory } from "../../lib/dialog";
import { useAction } from "../../lib/hooks";
import type { ProjectRecord } from "../../lib/types";

/// A new project is just a name plus a repository it maps to one-to-one --
/// the repository must already exist as a real git repo on disk (the same
/// requirement `beethoven project create` has always had; this UI doesn't
/// `git init` anything for you).
export function NewProjectDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (project: ProjectRecord) => void;
}) {
  const [name, setName] = useState("");
  const [repositoryPath, setRepositoryPath] = useState("");
  const [defaultBranch, setDefaultBranch] = useState("main");

  const create = useAction(
    ({ name, repositoryPath, defaultBranch }: { name: string; repositoryPath: string; defaultBranch: string }) =>
      api.createProject(name, repositoryPath, defaultBranch),
    { successMessage: (vars) => `${vars.name} created`, invalidateKeys: [["projects"]] },
  );

  const canSubmit = name.trim().length > 0 && repositoryPath.trim().length > 0 && defaultBranch.trim().length > 0;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <div className="w-full max-w-sm rounded-lg border border-slate-700 bg-slate-900 p-5 shadow-xl">
        <h2 className="text-sm font-semibold text-slate-100">New Project</h2>

        <div className="mt-4 flex flex-col gap-3">
          <Field label="Name">
            <input
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Markdown Viewer"
              className="w-full rounded-md border border-slate-700 bg-slate-950 px-2 py-1.5 text-sm text-slate-200"
            />
          </Field>
          <Field label="Repository Path" hint="Must already be a git repository on disk.">
            <div className="flex items-center gap-2">
              <input
                value={repositoryPath}
                onChange={(e) => setRepositoryPath(e.target.value)}
                placeholder="/Users/you/code/markdown-viewer"
                className="flex-1 rounded-md border border-slate-700 bg-slate-950 px-2 py-1.5 text-sm text-slate-200"
              />
              <button
                type="button"
                onClick={async () => {
                  const picked = await pickDirectory("Choose Repository", repositoryPath || undefined);
                  if (picked) setRepositoryPath(picked);
                }}
                className="whitespace-nowrap rounded-md border border-slate-700 px-2 py-1.5 text-xs text-slate-300 hover:bg-slate-800"
              >
                Browse…
              </button>
            </div>
          </Field>
          <Field label="Default Branch">
            <input
              value={defaultBranch}
              onChange={(e) => setDefaultBranch(e.target.value)}
              className="w-full rounded-md border border-slate-700 bg-slate-950 px-2 py-1.5 text-sm text-slate-200"
            />
          </Field>
        </div>

        <div className="mt-5 flex justify-end gap-2">
          <button
            onClick={onClose}
            disabled={create.isPending}
            className="rounded-md border border-slate-700 px-3 py-1.5 text-xs text-slate-300 hover:bg-slate-800 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            onClick={() =>
              create.mutate(
                { name: name.trim(), repositoryPath: repositoryPath.trim(), defaultBranch: defaultBranch.trim() },
                { onSuccess: onCreated },
              )
            }
            disabled={!canSubmit || create.isPending}
            className="rounded-md bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {create.isPending ? "Creating…" : "Create Project"}
          </button>
        </div>
      </div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs uppercase tracking-wide text-slate-500">{label}</span>
      {children}
      {hint && <span className="text-xs text-slate-500">{hint}</span>}
    </label>
  );
}
