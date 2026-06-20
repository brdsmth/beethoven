import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { Decision, InboxItem } from "../../lib/types";
import { ActionButton, InboxCardShell, RespondField } from "./InboxCardShell";

export function DecisionCard({ item, decision }: { item: InboxItem; decision: Decision | undefined }) {
  const respond = useAction((response: string) => api.answerInboxItem(item.id, response), {
    successMessage: "Decision resolved",
    invalidateKeys: [["inbox", item.project_id], ["project", item.project_id], ["decisions"]],
  });

  return (
    <InboxCardShell
      kind="decision"
      title={decision?.question ?? item.decision_question ?? "Decision"}
      createdAt={item.created_at}
      actions={
        <>
          <ActionButton label="Approve" onClick={() => respond.mutate("approved")} pending={respond.isPending} />
          <ActionButton
            label="Reject"
            tone="danger"
            onClick={() => respond.mutate("rejected")}
            pending={respond.isPending}
          />
          <RespondField label="Custom Response" onSubmit={(value) => respond.mutate(value)} pending={respond.isPending} />
        </>
      }
    >
      <p>{decision?.context ?? item.prompt}</p>
    </InboxCardShell>
  );
}
