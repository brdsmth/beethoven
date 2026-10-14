import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { Toaster } from "./Toaster";

/// Flat, no mode toggle -- most of what used to live behind a CEO/Operator
/// split (Tasks, Workspaces, Diffs, Prompts, Runtime's Workers/Logs) turned
/// out to be "pick a project, then view something already duplicated
/// inside Project Detail's tabs." Artifacts is deliberately not a nav item
/// here -- it's a secondary, linked-to view (from Home), not a primary one.
const NAV = [
  { to: "/", label: "Home" },
  { to: "/projects", label: "Projects" },
  { to: "/inbox", label: "Inbox" },
];

export function AppShell({ children }: { children: ReactNode }) {
  const { data: inbox } = useQuery({ queryKey: ["inbox"], queryFn: () => api.getInbox() });
  const openInboxCount = (inbox ?? []).filter((i) => i.status === "open").length;

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

        <ul className="flex flex-col gap-1">
          {NAV.map((item) => (
            <li key={item.to}>
              <NavLink
                to={item.to}
                end={item.to === "/"}
                className={({ isActive }) =>
                  `flex items-center justify-between rounded-md px-3 py-2 text-sm transition-colors ${
                    isActive
                      ? "bg-indigo-500/20 text-indigo-300"
                      : "text-slate-400 hover:bg-slate-800 hover:text-slate-200"
                  }`
                }
              >
                <span>{item.label}</span>
                {item.to === "/inbox" && openInboxCount > 0 && (
                  <span className="text-xs text-amber-400">{openInboxCount}</span>
                )}
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
