import { Link } from "react-router-dom";

/// The shared shape for every "nothing here" state in the app: a short,
/// specific message plus -- whenever there's somewhere the user could
/// actually go to fix it -- a link to get there. Replaces the old pattern
/// of a bare "No X yet." with no path forward.
export function EmptyState({
  message,
  actionLabel,
  actionTo,
}: {
  message: string;
  actionLabel?: string;
  actionTo?: string;
}) {
  return (
    <div className="flex flex-col items-start gap-1.5 text-sm text-slate-500">
      <p>{message}</p>
      {actionLabel && actionTo && (
        <Link to={actionTo} className="text-indigo-400 hover:underline">
          {actionLabel} →
        </Link>
      )}
    </div>
  );
}
