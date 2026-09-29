import { readFileSync, appendFileSync } from "node:fs";
export default function () {
  if (readFileSync("d77-order", "utf8") !== "A") throw new Error("B admitted before A");
  appendFileSync("d77-order", "B");
}
