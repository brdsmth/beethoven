import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { DecisionCard } from "../components/inbox/DecisionCard";
import { QuestionCard } from "../components/inbox/QuestionCard";
import { ApprovalCard } from "../components/inbox/ApprovalCard";
import { AlertCard } from "../components/inbox/AlertCard";

/// The single place every decision/question/approval/alert lives, across
/// every project -- absorbs what used to be a separate per-project Inbox
/// plus a separate cross-project Decisions page. Each card shows its own
/// project_slug now that this is no longer implicitly scoped to one
/// project via a picker.
export function InboxPage() {
  const { data: inbox } = useQuery({ queryKey: ["inbox"], queryFn: () => api.getInbox() });
  const { data: decisions } = useQuery({ queryKey: ["decisions"], queryFn: api.getDecisions });
  const decisionsById = new Map((decisions ?? []).map((d) => [d.id, d]));

  const openItems = (inbox ?? []).filter((i) => i.status === "open");

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Inbox</h1>

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
