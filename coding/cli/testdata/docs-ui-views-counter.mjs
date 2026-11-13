// docs-ui-views-counter.mjs: written from the documentation page ui-views alone (TestDocsUIViewsPageAloneTeachesACounter). A counter with a plain view, a web alternative and, for a client that draws scenes, a chart.
export default function counter(pi) {
  let count = 0;
  let mount = null;
  let mounting = false;
  let scene = false;

  const button = () => ({ kind: "mouse-region", id: "add", region: { types: ["press"] }, children: [{ kind: "text", text: "[ Add ]" }] });
  const plain = () => ({ root: { kind: "vstack", children: [{ kind: "text", text: `Count: ${count}` }, button()] } });
  const web = () => {
    const children = [{ kind: "markdown", text: `# Counter\n\nThe count is **${count}**.` }];
    if (scene) children.push({ kind: "box", id: "chart" });
    children.push(button());
    return { root: { kind: "vstack", children } };
  };
  const drawing = () => ({
    profile: "scene",
    node: "chart",
    viewport: { x: 0, y: 0, width: 100, height: 10, fit: "contain" },
    shapes: [{ kind: "rect", x: 0, y: 0, width: count * 10 + 10, height: 10, fill: "#3366ff" }],
    regions: [{ key: "add", shape: { kind: "rect", x: 0, y: 0, width: 100, height: 10 }, gestures: ["activate"], intent: "counter.add-chart", name: "Add one" }],
  });
  const answer = { type: "object", properties: { count: { type: "integer" } }, required: ["count"] };
  const content = () => {
    const alternative = { when: { any: [{ all: [{ claim: "client.kind", op: "equals", value: "web" }] }] }, view: web() };
    if (scene) alternative.drawing = drawing();
    return {
      view: plain(),
      alternatives: [alternative],
      // A region's intent has no nodeKey, so the chart raises an intent of its own.
      intents: [{ name: "counter.add", nodeKey: "add", arg: { type: "object" }, answer }, ...(scene ? [{ name: "counter.add-chart", arg: { type: "object" }, answer }] : [])],
      a11y: [{ node: "add", name: "Add one", role: "button" }],
      focus: [{ node: "add", request: "mount" }],
      fallback: { kind: "text", text: `Count: ${count}` },
    };
  };

  const show = async (abilities, ctx) => {
    if (!abilities || mount || mounting || !abilities.slots.includes("panel")) return;
    scene = abilities.drawing?.profiles.includes("scene") ?? false;
    mounting = true;
    try {
      mount = (await ctx.ui.mountView({ key: "counter", slot: "panel", ...content() })).mount;
    } catch (err) {
      ctx.ui.notify(`counter: ${err.code ?? err.message}`, "error");
    } finally {
      mounting = false;
    }
  };
  pi.onViewAbilities(show);
  pi.on("session_start", (_event, ctx) => show(ctx.ui.viewAbilities(), ctx));

  pi.onViewIntent(async (inv, ctx) => {
    if (inv.intent !== "counter.add" && inv.intent !== "counter.add-chart") return { outcome: "refused", error: "unknown intent" };
    count += 1;
    mount = (await ctx.ui.updateView(mount, content())).mount;
    return { outcome: "done", answer: { count } };
  });
}
