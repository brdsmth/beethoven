export function humanizeEventType(eventType: string): string {
  return eventType.replace(/([a-z0-9])([A-Z])/g, "$1 $2");
}

/// Fields worth showing as a one-line detail next to a humanized event
/// type, in priority order -- without this, "Task Created" x15 in a feed
/// are indistinguishable from each other even though the payload already
/// carries exactly what would tell them apart.
const EVENT_DETAIL_FIELDS = ["title", "question", "name", "reason", "prompt", "summary", "branch"] as const;

/// Best-effort, no extra network calls: pulls a short human-meaningful
/// detail straight out of an event's own payload. Returns null if the
/// payload doesn't carry any of the known fields (e.g. it only has a bare
/// task_id) -- the event type alone is still shown in that case.
export function eventDetail(payload: unknown): string | null {
  if (typeof payload !== "object" || payload === null) return null;
  const record = payload as Record<string, unknown>;
  for (const field of EVENT_DETAIL_FIELDS) {
    const value = record[field];
    if (typeof value === "string" && value.trim().length > 0) {
      return value.length > 80 ? `${value.slice(0, 80)}…` : value;
    }
  }
  return null;
}

export function formatRelativeTime(iso: string): string {
  const ms = Date.now() - Date.parse(iso);
  const seconds = Math.round(ms / 1000);
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  return `${days}d ago`;
}
