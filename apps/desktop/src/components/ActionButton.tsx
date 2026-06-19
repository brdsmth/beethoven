export function ActionButton({
  label,
  onClick,
  pending,
  disabled,
  tone = "default",
  title,
}: {
  label: string;
  onClick: () => void;
  pending?: boolean;
  disabled?: boolean;
  tone?: "default" | "danger";
  title?: string;
}) {
  return (
    <button
      onClick={onClick}
      disabled={pending || disabled}
      title={title}
      className={`rounded-md border px-3 py-1 text-xs transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
        tone === "danger"
          ? "border-rose-700 text-rose-300 hover:bg-rose-900/40"
          : "border-slate-700 text-slate-300 hover:bg-slate-800"
      }`}
    >
      {pending ? "Working…" : label}
    </button>
  );
}
