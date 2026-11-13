// A fixture of the page tests (controls_test.go): `/scene mount <points>` mounts a panel whose drawing is a scene of
// every shape kind with <points> points in its polylines, coloured by theme tokens so that it reads in both schemes, and regions that raise intents: `surface` (activate, drag,
// release) pans the scene by the drag and remembers where it was activated, `zoom` and `pin` are buttons. The handler
// redraws the mount and answers, so a test measures a whole round trip.
export default function scene(pi) {
  const model = { x: 0, y: 0, zoom: 1, at: "", released: 0, picked: 0, points: 0 };
  let mount;
  // A walk that stays inside the viewport and is the same on every run.
  const roads = () => {
    const lines = [];
    let seed = 7, x = 480, y = 320;
    const next = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;
    for (let done = 0; done < model.points; ) {
      const points = [];
      for (let i = 0; i < 1000 && done < model.points; i++, done++) {
        x = Math.min(950, Math.max(10, x + (next() - 0.5) * 40));
        y = Math.min(630, Math.max(10, y + (next() - 0.5) * 40));
        points.push({ x: Math.round(x * 10) / 10, y: Math.round(y * 10) / 10 });
      }
      lines.push({ kind: "polyline", key: `road-${lines.length}`, points, stroke: { color: "muted", width: 1.5 } });
    }
    return lines;
  };
  const number = { type: "number" }, string = { type: "string" };
  const content = () => ({
    view: {
      root: {
        kind: "vstack",
        children: [
          { kind: "text", text: `scene ${model.x},${model.y} zoom ${model.zoom} at ${model.at} released ${model.released} picked ${model.picked}`, paddingX: 0, paddingY: 0 },
          { kind: "box", id: "map", paddingX: 0, paddingY: 0, children: [{ kind: "text", text: "the map" }] },
        ],
      },
    },
    drawing: {
      profile: "scene",
      node: "map",
      viewport: { x: 0, y: 0, width: 960, height: 640, fit: "contain" },
      shapes: [
        { kind: "rect", key: "land", x: 0, y: 0, width: 960, height: 640, radius: 8, fill: "toolPendingBg" },
        { kind: "group", key: "roads", transform: [model.zoom, 0, 0, model.zoom, model.x, model.y], children: roads() },
        { kind: "path", key: "route", commands: [{ op: "move", x: 100, y: 500 }, { op: "line", x: 300, y: 420 }, { op: "quad", x1: 400, y1: 380, x: 480, y: 320 }, { op: "cubic", x1: 520, y1: 300, x2: 560, y2: 280, x: 600, y: 300 }, { op: "close" }], stroke: { color: "success", width: 3 }, opacity: 0.8 },
        { kind: "line", key: "scale", x1: 40, y1: 600, x2: 140, y2: 600, stroke: { color: "text", width: 2 } },
        { kind: "polyline", key: "park", closed: true, points: [{ x: 700, y: 100 }, { x: 800, y: 100 }, { x: 760, y: 180 }], fill: "toolSuccessBg" },
        { kind: "ellipse", key: "pin", cx: 480 + model.x, cy: 320 + model.y, rx: 8, ry: 8, fill: "accent", stroke: { color: "#ffffff", width: 2 } },
        { kind: "text", key: "label", x: 480 + model.x, y: 300 + model.y, text: "Harbour", size: 14, align: "center", fill: "text" },
        { kind: "group", key: "zoom-face", children: [{ kind: "rect", x: 900, y: 20, width: 40, height: 40, radius: 6, fill: "selectedBg", stroke: { color: "border", width: 1 } }, { kind: "text", x: 920, y: 48, text: "+", size: 24, align: "center", fill: "text" }] },
        { kind: "text", key: "credit", x: 950, y: 630, text: "© OpenStreetMap contributors", size: 11, align: "end", fill: "muted" },
      ],
      regions: [
        { key: "surface", shape: { kind: "rect", x: 0, y: 0, width: 960, height: 640 }, gestures: ["activate", "drag", "release"], intent: "scene.touch", name: "Map", role: "group" },
        { key: "zoom", shape: { kind: "rect", x: 900, y: 20, width: 40, height: 40, radius: 6 }, gestures: ["activate"], intent: "scene.zoom", name: "Zoom in" },
        { key: "pin", shape: { kind: "ellipse", cx: 480 + model.x, cy: 320 + model.y, rx: 12, ry: 12 }, gestures: ["activate"], intent: "scene.pick", name: "Harbour" },
      ],
    },
    intents: [
      { name: "scene.touch", arg: { type: "object", properties: { gesture: string, region: string, x: number, y: number, dx: number, dy: number }, required: ["gesture"] } },
      { name: "scene.zoom", arg: { type: "object" } },
      { name: "scene.pick", arg: { type: "object", properties: { region: string } } },
    ],
    fallback: { kind: "text", text: "This client draws no scene." },
  });

  pi.registerCommand("scene", {
    description: "Mount the test scene",
    handler: async (args, ctx) => {
      try {
        model.points = Number(args.trim().split(/\s+/)[1] ?? 0);
        mount = (await ctx.ui.mountView({ key: "scene", slot: "panel", title: "Harbour", ...content() })).mount;
      } catch (err) {
        ctx.ui.notify(`scene: ${err.code ?? err.message}`, "error");
      }
    },
  });

  pi.onViewIntent(async (inv, ctx) => {
    const { arg } = inv;
    if (inv.intent === "scene.zoom") model.zoom *= 2;
    else if (inv.intent === "scene.pick") model.picked++;
    else if (arg.gesture === "drag") {
      model.x = Math.round((model.x + arg.dx) * 1000) / 1000;
      model.y = Math.round((model.y + arg.dy) * 1000) / 1000;
    } else if (arg.gesture === "release") model.released++;
    else model.at = `${arg.x},${arg.y}`;
    mount = (await ctx.ui.updateView(mount, content())).mount;
    return { outcome: "done" };
  });
}
