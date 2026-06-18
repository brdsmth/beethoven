import type { ProjectDetail as ProjectDetailData } from "../../lib/types";
import { Panel } from "../Panel";

export function ProjectDetail({ project }: { project: ProjectDetailData }) {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold text-slate-100">{project.name}</h1>
          <div className="text-xs text-slate-500">{project.slug}</div>
        </div>
        <span className={project.status === "active" ? "text-emerald-400" : "text-slate-500"}>
          {project.status}
        </span>
      </div>

      <Panel title="Initiatives">
        {project.initiatives.length === 0 ? (
          <div className="text-sm text-slate-500">No initiatives.</div>
        ) : (
          <div className="flex flex-col gap-4">
            {project.initiatives.map((initiative) => (
              <div key={initiative.id} className="rounded-md border border-slate-800 p-3">
                <div className="flex items-center justify-between">
                  <div className="font-medium text-slate-100">{initiative.title}</div>
                  <span className="text-xs uppercase text-slate-500">{initiative.status}</span>
                </div>
                <p className="mt-1 text-sm text-slate-400">{initiative.description}</p>

                {initiative.epics.length > 0 && (
                  <div className="mt-3">
                    <div className="text-xs uppercase tracking-wide text-slate-500">Epics</div>
                    <ul className="mt-1 flex flex-col gap-1">
                      {initiative.epics.map((epic) => (
                        <li key={epic.id} className="text-sm text-slate-300">
                          {epic.title}{" "}
                          <span className="text-xs text-slate-500">({epic.tasks.length} tasks)</span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                {initiative.decisions.length > 0 && (
                  <div className="mt-3">
                    <div className="text-xs uppercase tracking-wide text-slate-500">Decisions</div>
                    <ul className="mt-1 flex flex-col gap-1">
                      {initiative.decisions.map((d) => (
                        <li key={d.id} className="text-sm text-slate-300">
                          {d.title}{" "}
                          <span
                            className={`text-xs ${d.status === "open" ? "text-amber-400" : "text-slate-500"}`}
                          >
                            ({d.status})
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </Panel>

      <Panel title="Goals">
        {project.goals.length === 0 ? (
          <div className="text-sm text-slate-500">No goals.</div>
        ) : (
          <div className="flex flex-col gap-4">
            {project.goals.map((goal) => (
              <div key={goal.id} className="rounded-md border border-slate-800 p-3">
                <div className="flex items-center justify-between">
                  <div className="font-medium text-slate-100">{goal.title}</div>
                  <span className="text-xs uppercase text-slate-500">{goal.status}</span>
                </div>
                <ul className="mt-2 flex flex-col gap-1">
                  {goal.tasks.map((t) => (
                    <li key={t.id} className="flex items-center justify-between text-sm text-slate-300">
                      <span>{t.title}</span>
                      <span className="text-xs text-slate-500">{t.status}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </div>
  );
}
