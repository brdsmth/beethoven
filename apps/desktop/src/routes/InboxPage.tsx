import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useSelectedProject } from "../lib/hooks";
import { ProjectPicker } from "../components/ProjectPicker";
import { DecisionCard } from "../components/inbox/DecisionCard";
import { QuestionCard } from "../components/inbox/QuestionCard";
import { ApprovalCard } from "../components/inbox/ApprovalCard";
import { AlertCard } from "../components/inbox/AlertCard";

export function InboxPage() {
  const { projects, selectedProjectId, setSelectedProjectId } = useSelectedProject();

  const { data: inbox } = useQuery({
    queryKey: ["inbox", selectedProjectId],
    queryFn: () => api.getInbox(selectedProjectId as string),
    enabled: !!selectedProjectId,
  });
  const { data: project } = useQuery({
    queryKey: ["project", selectedProjectId],
    queryFn: () => api.getProject(selectedProjectId as string),
    enabled: !!selectedProjectId,
  });

  const decisionsById = new Map((project?.initiatives ?? []).flatMap((i) => i.decisions).map((d) => [d.id, d]));

  if (!selectedProjectId) {
    return <div className="text-sm text-slate-500">No projects yet.</div>;
  }

  const openItems = (inbox ?? []).filter((i) => i.status === "open");

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold text-slate-100">Inbox</h1>
        <ProjectPicker projects={projects} selectedProjectId={selectedProjectId} onChange={setSelectedProjectId} />
      </div>

      {openItems.length === 0 ? (
        <div className="text-sm text-slate-500">Inbox is empty.</div>
      ) : (
        <div className="flex flex-col gap-3">
          {openItems.map((item) => {
            switch (item.kind) {
              case "decision":
                return (
                  <DecisionCard
                    key={item.id}
                    item={item}
                    decision={item.decision_id ? decisionsById.get(item.decision_id) : undefined}
                  />
                );
              case "question":
                return <QuestionCard key={item.id} item={item} />;
              case "approval":
                return <ApprovalCard key={item.id} item={item} />;
              case "alert":
                return <AlertCard key={item.id} item={item} />;
            }
          })}
        </div>
      )}
    </div>
  );
}
