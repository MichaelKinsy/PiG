// A demo extension for the reference web client: `/ui-demo` mounts one panel that uses every view node kind that carries
// an id, with the a11y and focus records that name them and one intent per node.
//
//   node bridge.mjs -- -e ./demo-extension.mjs        then type /ui-demo in the page
export default function uiDemo(pi) {
  const colors = ["red", "green", "blue"];
  const model = { color: "red", name: "", taps: 0, wrap: "on", notes: "" };
  let mount;

  const view = () => ({
    root: {
      kind: "vstack",
      children: [
        { kind: "text", text: `Hello ${model.name || "there"}: ${model.color}, ${model.taps} taps, wrap ${model.wrap}` },
        { kind: "select-list", id: "colors", items: colors.map((value) => ({ value, label: value, description: `pick ${value}` })), selectedIndex: colors.indexOf(model.color) },
        { kind: "input", id: "name", value: model.name, placeholder: "Your name, then Enter" },
        { kind: "mouse-region", id: "tap", region: { types: ["press"] }, children: [{ kind: "text", text: "Tap here" }] },
        { kind: "settings-list", id: "prefs", items: [{ id: "wrap", label: "Wrap", currentValue: model.wrap, values: ["on", "off"] }] },
        { kind: "editor", id: "notes", value: model.notes },
        { kind: "scroll-view", id: "log", children: Array.from({ length: 12 }, (_, i) => ({ kind: "text", text: `log line ${i + 1}` })) },
      ],
    },
  });
  const text = { type: "object", properties: { value: { type: "string" } }, required: ["value"] };
  const content = () => ({
    view: view(),
    intents: [
      { name: "demo.pick", nodeKey: "colors", arg: text },
      { name: "demo.rename", nodeKey: "name", arg: text },
      { name: "demo.tap", nodeKey: "tap", arg: { type: "object" }, answer: { type: "object", properties: { taps: { type: "integer" } }, required: ["taps"] } },
      { name: "demo.pref", nodeKey: "prefs", arg: { type: "object", properties: { item: { type: "string" }, value: { type: "string" } }, required: ["item", "value"] } },
      { name: "demo.note", nodeKey: "notes", arg: text },
      { name: "demo.close", arg: { type: "object" } },
    ],
    a11y: [
      { node: "colors", name: "Color", role: "list" },
      { node: "name", name: "Name", description: "Shown in the greeting" },
      { node: "tap", name: "Tap counter", role: "button" },
      { node: "prefs", name: "Preferences" },
      { node: "notes", name: "Notes" },
      { node: "log", name: "Log", role: "region" },
    ],
    focus: [{ node: "colors", request: "mount", restore: "previous" }, { node: "name" }, { node: "tap" }, { node: "prefs" }, { node: "notes" }, { node: "log" }],
    fallback: { kind: "text", text: "The demo panel does not fit this client." },
  });

  pi.registerCommand("ui-demo", {
    description: "Mount the plugin UI demo panel",
    handler: async (_args, ctx) => {
      try {
        mount = (await ctx.ui.mountView({ key: "demo", slot: "panel", ...content() })).mount;
      } catch (err) {
        ctx.ui.notify(`ui-demo: ${err.code ?? err.message}`, "error");
      }
    },
  });

  // The handler changes its state and redraws its own mount before it answers. An update the answering handler makes
  // belongs to the answer: the intent settles done, and the client is written the new view before the intent's reply.
  pi.onViewIntent(async (inv, ctx) => {
    let answer;
    if (inv.intent === "demo.pick" && colors.includes(inv.arg.value)) model.color = inv.arg.value;
    else if (inv.intent === "demo.rename") model.name = inv.arg.value;
    else if (inv.intent === "demo.tap") answer = { taps: ++model.taps };
    else if (inv.intent === "demo.pref" && inv.arg.item === "wrap") model.wrap = inv.arg.value;
    else if (inv.intent === "demo.note") model.notes = inv.arg.value;
    else if (inv.intent === "demo.close") {
      await ctx.ui.finishView(mount, { color: model.color, taps: model.taps });
      return { outcome: "done" };
    } else return { outcome: "refused", error: `no such choice for ${inv.intent}` };
    mount = (await ctx.ui.updateView(mount, content())).mount;
    return { outcome: "done", answer };
  });
}
