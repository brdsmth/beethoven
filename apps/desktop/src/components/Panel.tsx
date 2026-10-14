import type { ReactNode } from "react";

export function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-slate-800 bg-slate-900/40 p-4">
      <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-widest text-slate-500">{title}</h2>
      {children}
    </section>
  );
}
