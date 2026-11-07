import { Box, MouseRegion } from "@earendil-works/pi-tui";

// A custom entry renderer written as Pi's are: the card's expansion lives in a WeakMap keyed by the entry object. Pi renders the session
// manager's one object per entry on every rebuild of the transcript, so the state a click left survives the rebuild.
export default function (pi) {
  const states = new WeakMap();
  pi.registerEntryRenderer("click-entry", (entry, options) => {
    let state = states.get(entry);
    if (state === undefined) {
      state = { expanded: options.expanded };
      states.set(entry, state);
    }
    const view = state;
    const content = {
      render: () => [`entry:${entry.data}:${view.expanded ? "open" : "closed"}`, view.expanded ? `  details of ${entry.data}` : ""].filter((line) => line !== ""),
      invalidate() {},
    };
    const box = new Box(3, 0);
    box.addChild(new MouseRegion(content, (event) => {
      if (event.type !== "click" || event.button !== "left") return undefined;
      view.expanded = !view.expanded;
      return { handled: true };
    }));
    return box;
  });
}
