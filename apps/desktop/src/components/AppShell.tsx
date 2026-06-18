import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";

const NAV_ITEMS = [
  { to: "/", label: "Mission Control" },
  { to: "/projects", label: "Projects" },
  { to: "/inbox", label: "Inbox" },
  { to: "/agents", label: "Agents" },
  { to: "/timeline", label: "Timeline" },
  { to: "/settings", label: "Settings" },
];

export function AppShell({ children }: { children: ReactNode }) {
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
          {NAV_ITEMS.map((item) => (
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
      </nav>
      <main className="flex flex-1 flex-col overflow-hidden">
        <div data-tauri-drag-region className="h-9 shrink-0" />
        <div className="flex-1 overflow-y-auto px-6 pb-6">{children}</div>
      </main>
    </div>
  );
}
