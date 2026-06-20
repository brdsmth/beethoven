import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { useViewStore, type AppMode } from "../lib/store";
import { Toaster } from "./Toaster";

// Work, dependencies, progress, decisions -- not agents. Agents/Agent
// Health/Timeline used to be top-level nav; they're now Runtime -> Workers
// and Runtime -> Logs, reachable but no longer primary.
const CEO_NAV = [
  { to: "/", label: "Mission Control" },
  { to: "/projects", label: "Projects" },
  { to: "/tasks", label: "Tasks" },
  { to: "/inbox", label: "Inbox" },
  { to: "/decisions", label: "Decisions" },
];

const OPERATOR_NAV = [
  { to: "/runtime", label: "Runtime" },
  { to: "/workspaces", label: "Workspaces" },
  { to: "/diffs", label: "Diffs" },
  { to: "/prompts", label: "Prompts" },
];

export function AppShell({ children }: { children: ReactNode }) {
  const mode = useViewStore((s) => s.mode);
  const setMode = useViewStore((s) => s.setMode);
  const navItems = mode === "ceo" ? CEO_NAV : OPERATOR_NAV;

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-slate-950 text-slate-100">
      <nav className="flex w-52 shrink-0 flex-col border-r border-slate-800 bg-slate-900/60 p-3">
        {/* Reserves space for the macOS overlay title bar's traffic-light
            buttons and lets the window be dragged from here, since the
            sidebar's own background -- not a native title bar -- now
            extends to the top of the window. */}
        <div data-tauri-drag-region className="-mx-3 -mt-3 h-9 shrink-0" />
        <div className="mb-4 px-2 text-sm font-semibold tracking-wide text-slate-300">
          Beethoven
        </div>

        <ModeToggle mode={mode} onChange={setMode} />

        <ul className="mt-3 flex flex-col gap-1">
          {navItems.map((item) => (
            <li key={item.to}>
              <NavLink
                to={item.to}
                end={item.to === "/"}
                className={({ isActive }) =>
                  `block rounded-md px-3 py-2 text-sm transition-colors ${
                    isActive
                      ? "bg-indigo-500/20 text-indigo-300"
                      : "text-slate-400 hover:bg-slate-800 hover:text-slate-200"
                  }`
                }
              >
                {item.label}
              </NavLink>
            </li>
          ))}
        </ul>

        <div className="mt-auto pt-3">
          <NavLink
            to="/settings"
            className={({ isActive }) =>
              `block rounded-md px-3 py-2 text-sm transition-colors ${
                isActive
                  ? "bg-indigo-500/20 text-indigo-300"
                  : "text-slate-400 hover:bg-slate-800 hover:text-slate-200"
              }`
            }
          >
            Settings
          </NavLink>
        </div>
      </nav>
      <main className="flex flex-1 flex-col overflow-hidden">
        <div data-tauri-drag-region className="h-9 shrink-0" />
        <div className="flex-1 overflow-y-auto px-6 pb-6">{children}</div>
      </main>
      <Toaster />
    </div>
  );
}

function ModeToggle({ mode, onChange }: { mode: AppMode; onChange: (mode: AppMode) => void }) {
  return (
    <div className="grid grid-cols-2 gap-1 rounded-md border border-slate-800 bg-slate-950/60 p-1 text-xs">
      <button
        onClick={() => onChange("ceo")}
        className={`rounded px-2 py-1 transition-colors ${
          mode === "ceo" ? "bg-indigo-500/20 text-indigo-300" : "text-slate-500 hover:text-slate-300"
        }`}
      >
        CEO
      </button>
      <button
        onClick={() => onChange("operator")}
        className={`rounded px-2 py-1 transition-colors ${
          mode === "operator" ? "bg-indigo-500/20 text-indigo-300" : "text-slate-500 hover:text-slate-300"
        }`}
      >
        Operator
      </button>
    </div>
  );
}
