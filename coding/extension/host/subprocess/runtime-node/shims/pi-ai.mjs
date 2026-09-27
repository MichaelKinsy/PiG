// The pi-ai root as Pi serves it to extensions: its compat entry point
// (core/extensions/virtual-modules.ts resolves "@earendil-works/pi-ai" and
// "@earendil-works/pi-ai/compat" to one module). This is Pi's own code copied
// verbatim from the pinned release (automation/gen/vendor-pi-dist.sh); only
// the builtin API implementations behind it run in PiG's host (D74).
export * from "./pi-dist/pi-ai/compat.js";

// pig divergence (D73): Pi runs these session-resource cleanups when its
// agent session ends. Sessions live in PiG's Go host, so a cleanup registered
// in the extension process would never run. The values are importable so no
// extension fails at link time, and throw a descriptive error only when
// called.
function hostOnlyAiFunction(name) {
  const fn = function () {
    throw new Error(`${name} is not available to extensions running in PiG: agent sessions run in PiG's host (see DIVERGENCES.md D73)`);
  };
  Object.defineProperty(fn, "name", { value: name });
  return fn;
}
export const cleanupSessionResources = hostOnlyAiFunction("cleanupSessionResources");
export const registerSessionResourceCleanup = hostOnlyAiFunction("registerSessionResourceCleanup");
