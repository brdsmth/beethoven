import { useState } from "react";
import { ActionButton } from "./ActionButton";

/** Inline expand-to-text-field action, used anywhere a free-text response/reason is needed. */
export function RespondField({
  label,
  onSubmit,
  pending,
}: {
  label: string;
  onSubmit: (response: string) => void;
  pending?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState("");

  if (!open) {
    return <ActionButton label={label} onClick={() => setOpen(true)} />;
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (!value.trim()) return;
        onSubmit(value.trim());
      }}
      className="flex w-full items-center gap-2"
    >
      <input
        autoFocus
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder={label}
        className="flex-1 rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200"
      />
      <ActionButton label="Submit" onClick={() => value.trim() && onSubmit(value.trim())} pending={pending} />
      <button
        type="button"
        onClick={() => setOpen(false)}
        className="text-xs text-slate-500 hover:text-slate-300"
      >
        Cancel
      </button>
    </form>
  );
}
