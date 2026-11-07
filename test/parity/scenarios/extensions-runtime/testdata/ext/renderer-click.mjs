import { Box, MouseRegion } from "@earendil-works/pi-tui";

// A custom message renderer written as Pi's are: the card's expansion lives in a WeakMap keyed by the message object
// (which Pi hands the renderer on every rebuild), a MouseRegion over the card toggles it on a left click, and a new
// global expansion (the expand key) resets the local choice.
export default function (pi) {
  const states = new WeakMap();
  pi.registerMessageRenderer("click-card", (message, options) => {
    let state = states.get(message);
    if (state === undefined || state.global !== options.expanded) {
      state = { global: options.expanded, expanded: options.expanded };
      states.set(message, state);
    }
    const view = state;
    const content = {
      render: () => [`card:${message.content}:${view.expanded ? "open" : "closed"}`, view.expanded ? `  details of ${message.content}` : ""].filter((line) => line !== ""),
      invalidate() {},
    };
    // The region sits in a padded Box, as in Pi's own cards: a click on the padding is outside any MouseRegion and falls through to selection.
    const box = new Box(3, 0);
    box.addChild(new MouseRegion(content, (event) => {
      if (event.type !== "click" || event.button !== "left") return undefined;
      view.expanded = !view.expanded;
      return { handled: true };
    }));
    return box;
  });
}
