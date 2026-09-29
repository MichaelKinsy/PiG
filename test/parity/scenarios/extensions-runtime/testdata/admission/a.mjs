import { readFileSync, writeFileSync } from "node:fs";
export default function (pi) {
  writeFileSync("d77-order", "A");
  pi.events.on("admission:probe", data => { data.a = true; });
  pi.events.on("admission:loading", data => {
    try { pi.getActiveTools(); data.bound = true; }
    catch { data.bound = false; }
  });
  pi.registerCommand("admission-probe", { handler: async () => {
    const data = {};
    pi.events.emit("admission:probe", data);
    console.error(`order=${JSON.stringify(readFileSync("d77-order", "utf8"))} shared=${JSON.stringify(data)}`);
  }});
}
