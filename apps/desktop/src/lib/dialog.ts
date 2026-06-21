import { open } from "@tauri-apps/plugin-dialog";

/// Opens a native folder picker, returning the selected path or `null` if
/// the user cancelled. The repository path fields use this instead of
/// expecting someone to type/paste an absolute path by hand.
export async function pickDirectory(title: string, defaultPath?: string): Promise<string | null> {
  const selected = await open({ title, directory: true, multiple: false, defaultPath });
  return typeof selected === "string" ? selected : null;
}
