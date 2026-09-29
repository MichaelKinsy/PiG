import { probeFailedAPI } from "./probe.mjs";

export default async function (pi) {
  const state = globalThis.__factoryFailureProbe;
  const timerError = await state.lateResult;
  let survivorEvents = 0;
  let lateEventCalls = 0;
  const unsubscribe = pi.events.on("factory-failure", () => { survivorEvents++; });
  pi.events.emit("factory-failure", undefined);
  const results = await probeFailedAPI(state.api, () => { lateEventCalls++; });
  state.unsubscribe();
  state.unsubscribe();
  pi.events.emit("factory-failure", undefined);
  unsubscribe();
  pi.registerCommand("factory-failure-report", {
    description: JSON.stringify({ flagDuringLoad: state.flagDuringLoad, eventCalls: state.eventCalls, survivorEvents, lateEventCalls, timerError, results }),
    handler: async () => {},
  });
}
