import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { crc32, deflateSync } from "node:zlib";

function pngChunk(type, body) {
  const header = Buffer.alloc(8);
  header.writeUInt32BE(body.length, 0);
  header.write(type, 4, "ascii");
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(Buffer.concat([header.subarray(4), body])), 0);
  return Buffer.concat([header, body, checksum]);
}
function createPng(width, height) {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0); ihdr.writeUInt32BE(height, 4); ihdr[8] = 8;
  const raw = Buffer.alloc((width + 1) * height);
  for (let row = 0; row < height; row++) raw.fill(row % 256, row * (width + 1) + 1, (row + 1) * (width + 1));
  return Buffer.concat([Buffer.from([0x89,0x50,0x4e,0x47,0x0d,0x0a,0x1a,0x0a]), pngChunk("IHDR", ihdr), pngChunk("IDAT", deflateSync(raw)), pngChunk("IEND", Buffer.alloc(0))]);
}
const bmp = Buffer.alloc(58);
bmp.write("BM"); bmp.writeUInt32LE(58, 2); bmp.writeUInt32LE(54, 10); bmp.writeUInt32LE(40, 14);
bmp.writeInt32LE(1, 18); bmp.writeInt32LE(1, 22); bmp.writeUInt16LE(1, 26); bmp.writeUInt16LE(24, 28); bmp.writeUInt32LE(4, 34); bmp[56] = 0xff;
const source = [
  {type: "text", text: "before echo-bridge: hello"},
  {type: "image", data: bmp.toString("base64"), mimeType: "image/bmp"},
  {type: "text", text: ""},
  {type: "image", data: createPng(2400, 100).toString("base64"), mimeType: "image/png"},
  {type: "text", text: "after"},
];
// The original contract asserts PNG dimensions, not encoder-specific compressed bytes. Every block and its position remains in this projection, including explicit empty text.
function describe(content) {
  return content.map(block => {
    if (block.type === "text") return {type: block.type, text: block.text};
    const data = Buffer.from(block.data, "base64");
    return {type: block.type, mimeType: block.mimeType, dimensions: block.mimeType === "image/bmp" ? [data.readInt32LE(18), data.readInt32LE(22)] : [data.readUInt32BE(16), data.readUInt32BE(20)]};
  });
}
export default function (pi) {
  const observed = {};
  pi.registerTool({name: "echo_bridge", label: "Ordered images", description: "Return ordered tool images", parameters: {type: "object", properties: {text: {type: "string"}}}, async execute() {
    return {content: source, details: {marker: "retained"}};
  }});
  pi.on("tool_result", event => {
    if (event.toolName !== "echo_bridge") return;
    observed.hook = describe(event.content);
    return {content: event.content};
  });
  pi.on("tool_execution_end", event => {
    if (event.toolName === "echo_bridge") observed.end = describe(event.result.content);
  });
  pi.on("context", event => {
    const message = event.messages.find(message => message.role === "toolResult");
    if (message) observed.context = describe(message.content);
  });
  pi.on("agent_end", (_event, ctx) => {
    const message = ctx.sessionManager.getBranch().find(entry => entry.type === "message" && entry.message.role === "toolResult")?.message;
    if (!message) throw new Error("persisted tool result missing");
    observed.history = describe(message.content);
    observed.details = message.details;
    writeFileSync(join(process.env.PARITY_ORDER_DIR, "ordered.json"), JSON.stringify(observed) + "\n");
  });
}
