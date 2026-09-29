import { readFileSync, appendFileSync } from "node:fs";
export default function (pi) {
  if (readFileSync("d77-order", "utf8") !== "AB") throw new Error("C admitted before B");
  const loading = {};
  pi.events.emit("admission:loading", loading);
  if (loading.bound !== false) throw new Error("host actions bound before factory loading finished");
  appendFileSync("d77-order", "C");
  pi.events.on("admission:probe", data => { data.c = true; });
}
