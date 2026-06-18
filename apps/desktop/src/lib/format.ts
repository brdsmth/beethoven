export function humanizeEventType(eventType: string): string {
  return eventType.replace(/([a-z0-9])([A-Z])/g, "$1 $2");
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
