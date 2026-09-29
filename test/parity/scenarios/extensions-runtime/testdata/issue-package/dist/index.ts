// Package entry imported through pi.extensions, not its decoy root index.
import { calculateCost } from "@earendil-works/pi-ai";
import { CancellableLoader } from "@earendil-works/pi-tui";
enum Phase { Loaded = "loaded" }
const factory = (pi: any) => {
  const model = { cost: { input: 2, output: 8, cacheRead: 1, cacheWrite: 3 } };
  const usage = { input: 1_000_000, output: 500_000, cacheRead: 200_000, cacheWrite: 100_000, cost: {} };
  calculateCost(model as any, usage as any);
  const description = JSON.stringify({ phase: Phase.Loaded, loader: typeof CancellableLoader, cost: usage.cost });
  pi.registerCommand("issue-package", { description, handler: async () => { console.log(description); } });
};
export { factory as default };
