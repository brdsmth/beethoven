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
      <main className="flex-1 overflow-y-auto p-6">{children}</main>
    </div>
  );
}
