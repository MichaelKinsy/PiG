// The registry check of pi-background-tasks@2.6.9 dist/extensions/anthropic-attribution.js (ISC): a session_start handler registers a streamSimple override for the built-in anthropic provider and requires the registry to report the installation as soon as registerProvider returns.
const ANTHROPIC_PROVIDER = "anthropic";

function captureProviderSnapshot(registry) {
  const effective = registry.getProvider(ANTHROPIC_PROVIDER);
  if (effective === undefined) throw new Error("the host exposes no effective anthropic provider to preserve");
  return {
    effective,
    legacy: registry.getRegisteredProviderConfig(ANTHROPIC_PROVIDER),
    native: registry.getRegisteredNativeProvider(ANTHROPIC_PROVIDER),
  };
}

function confirmProviderInstallation(registry, before) {
  const token = registry.getRegisteredProviderConfig(ANTHROPIC_PROVIDER);
  const native = registry.getRegisteredNativeProvider(ANTHROPIC_PROVIDER);
  const effective = registry.getProvider(ANTHROPIC_PROVIDER);
  if (token === before.legacy && native === before.native && effective === before.effective) return;
  if (token === undefined || token === before.legacy || native !== undefined || effective === undefined ||
      effective === before.effective || token.streamSimple === before.legacy?.streamSimple) {
    throw new Error("pi_anthropic_attribution_install_failed: host provider registration did not install the package transport atomically");
  }
}

export default function (pi) {
  pi.on("session_start", (_event, context) => {
    const registry = context.modelRegistry;
    const before = captureProviderSnapshot(registry);
    pi.registerProvider(ANTHROPIC_PROVIDER, {
      api: "anthropic-messages",
      streamSimple: () => { throw new Error("unused"); },
    });
    confirmProviderInstallation(registry, before);
  });
}
