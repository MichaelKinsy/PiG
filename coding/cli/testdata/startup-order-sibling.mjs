// A sibling extension loaded after startup-order-native-provider.mjs. Its factory finishes only when the Provider's auth check has run or 3 seconds have passed, then appends "sibling factory done" to $ORDER_LOG. In Pi 0.87.1 no Provider callback runs before every factory has finished (agent-session-services.ts:158-182), so the factory always waits the full time and its line comes first.
import { appendFileSync, existsSync, readFileSync } from "node:fs";

export default async function () {
  const log = process.env.ORDER_LOG;
  const deadline = Date.now() + 3000;
  while (Date.now() < deadline && !(existsSync(log) && readFileSync(log, "utf8").includes("check"))) {
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  appendFileSync(log, "sibling factory done\n");
}
