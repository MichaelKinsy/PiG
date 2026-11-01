// The control requests that carry contracts as data and references across the boundary (docs/plan/chord-plugins-node-evaluator.md
// 10.1 to 10.4).
import { operations as contractOperations } from "./contracts.mjs";

/**
 * The bridge of one driver: `port` is its control channel (`control(message)`), `wireError` turns an error into data and
 * `host()` is the process's host (undefined before `load`).
 */
export function createBridge({ port, wireError, host }) {
	return { operations: { ...contractOperations } };
}
