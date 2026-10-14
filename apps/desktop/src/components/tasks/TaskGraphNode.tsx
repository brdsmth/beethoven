export function TaskGraphNode({
  kicker,
  title,
  statusLabel,
  dotColor,
  statusColor,
  selected,
  onClick,
  disabled,
}: {
  kicker: string;
  title: string;
  statusLabel: string;
  dotColor: string;
  statusColor: string;
  selected: boolean;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`w-44 flex-none rounded-md border bg-slate-900/60 px-2.5 py-2 text-left text-xs transition-colors ${
        disabled
          ? "cursor-default border-slate-800 opacity-60"
          : selected
            ? "border-amber-500"
            : "border-slate-700 hover:border-slate-600"
      }`}
    >
      <div className="flex items-center gap-1.5">
        <span className={`h-1.5 w-1.5 flex-none rounded-full ${dotColor}`} />
        <span className="truncate text-[10px] uppercase tracking-wide text-slate-500">{kicker}</span>
      </div>
      <div className="mt-1 truncate text-slate-200">{title}</div>
      <div className={`mt-0.5 truncate text-[11px] ${statusColor}`}>{statusLabel}</div>
    </button>
  );
}

/// Connects two node columns left to right -- a hairline with a small
/// arrowhead, never a colored/animated line (status lives on the nodes).
export function TaskGraphEdge() {
  return (
    <div className="relative mx-1 h-px w-8 flex-none self-center bg-slate-700">
      <span className="absolute -top-[7px] -right-1 text-[10px] text-slate-600">▸</span>
    </div>
  );
}
