// A factory whose instance state tells two Hosts' instances in one shared process apart: counter counts this instance's calls, ping emits on the process's pi.events bus and received lists what this instance heard (pig additive (D232)).
export default function (pi) {
  let calls = 0;
  const heard = [];
  const text = (value) => ({ content: [{ type: "text", text: value }] });
  pi.events.on("shared-cells", (data) => heard.push(data.from));
  pi.registerTool({ name: "counter", label: "counter", description: "this instance's call count", parameters: { type: "object", properties: {} }, async execute() { return text(String(++calls)); } });
  pi.registerTool({ name: "ping", label: "ping", description: "emit on the event bus", parameters: { type: "object", properties: { name: { type: "string" } }, required: ["name"] }, async execute(_id, params) { pi.events.emit("shared-cells", { from: params.name }); return text("pinged"); } });
  pi.registerTool({ name: "received", label: "received", description: "what this instance heard", parameters: { type: "object", properties: {} }, async execute() { return text("received=" + heard.join(",")); } });
  pi.registerTool({ name: "listeners", label: "listeners", description: "the process's beforeExit listener count", parameters: { type: "object", properties: {} }, async execute() { return text("beforeExit=" + process.listenerCount("beforeExit")); } });
  pi.registerTool({ name: "virtual", label: "virtual", description: "register shared/<name>", parameters: { type: "object", properties: { name: { type: "string" } }, required: ["name"] }, async execute(_id, params) {
    pi.registerVirtualModel({ provider: "shared", id: params.name, name: params.name, route: () => ({ model: { provider: "p", id: "m" }, thinkingLevel: "off" }) });
    return text("registered");
  } });
  pi.registerTool({ name: "models", label: "models", description: "the shared/* models this instance's registry shows", parameters: { type: "object", properties: {} }, async execute(_id, _params, _signal, _update, ctx) {
    return text("models=" + ctx.modelRegistry.getAll().filter((model) => model.provider === "shared").map((model) => model.id).join(","));
  } });
}
