// Node extensions share the host terminal's measured geometry even though
// their stdout is a transport pipe. Packed members publish each resize once.
export function syncTerminalGeometry(ready) {
  if (ready?.mode !== "tui" || !(ready.width > 0) || !(ready.height > 0)) return;
  const changed = process.stdout.columns !== ready.width || process.stdout.rows !== ready.height;
  process.stdout.columns = ready.width;
  process.stdout.rows = ready.height;
  if (changed) process.stdout.emit("resize");
}
