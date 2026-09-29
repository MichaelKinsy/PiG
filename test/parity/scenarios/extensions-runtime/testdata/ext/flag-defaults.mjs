import { writeFileSync } from "node:fs";
export default function (pi) {
  pi.registerFlag("capture", { type: "string" });
  pi.registerFlag("first", { type: "boolean", default: true });
  pi.registerFlag("second", { type: "boolean", default: false });
  pi.registerFlag("word", { type: "string", default: "default" });
  pi.registerFlag("empty", { type: "string", default: "" });
  pi.registerFlag("unset", { type: "string" });
  pi.registerFlag("repeat", { type: "boolean", default: true });
  pi.registerFlag("repeat", { type: "boolean", default: false });
  const read = () => ["first", "second", "word", "empty", "unset", "repeat", "unregistered"].map(name => pi.getFlag(name));
  const duringFactory = read();
  pi.registerCommand("flag-defaults", { handler: async () => {
    writeFileSync(pi.getFlag("capture"), JSON.stringify({ duringFactory, duringCommand: read() }) + "\n");
  }});
}
