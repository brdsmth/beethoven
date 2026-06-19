import type { ReactNode } from "react";
import { ActionButton } from "../ActionButton";
import { RespondField } from "../RespondField";

export { ActionButton, RespondField };

const KIND_COLOR: Record<string, string> = {
  decision: "border-l-violet-500",
  question: "border-l-sky-500",
  approval: "border-l-amber-500",
  alert: "border-l-rose-500",
};

export function InboxCardShell({
  kind,
  title,
  createdAt,
  children,
  actions,
}: {
  kind: "decision" | "question" | "approval" | "alert";
  title: string;
  createdAt: string;
  children: ReactNode;
  actions: ReactNode;
}) {
  return (
    <div className={`rounded-lg border border-slate-800 border-l-4 bg-slate-900/40 p-4 ${KIND_COLOR[kind]}`}>
      <div className="flex items-center justify-between">
        <div className="text-xs uppercase tracking-wide text-slate-500">{kind}</div>
        <div className="text-xs text-slate-500">{new Date(createdAt).toLocaleString()}</div>
      </div>
      <div className="mt-1 font-medium text-slate-100">{title}</div>
      <div className="mt-2 text-sm text-slate-300">{children}</div>
      <div className="mt-3 flex flex-wrap items-center gap-2">{actions}</div>
    </div>
  );
}

