import type { Decision, InboxItem } from "../../lib/types";
import { InboxCardShell, DisabledActionButton } from "./InboxCardShell";

export function DecisionCard({ item, decision }: { item: InboxItem; decision: Decision | undefined }) {
  return (
    <InboxCardShell
      kind="decision"
      title={decision?.title ?? item.decision_title ?? "Decision"}
      createdAt={item.created_at}
      actions={
        <>
          <DisabledActionButton label="Approve" />
          <DisabledActionButton label="Reject" />
          <DisabledActionButton label="Answer" />
        </>
      }
    >
      <p>{decision?.context ?? item.prompt}</p>
    </InboxCardShell>
  );
}
