// Ports packages/durable/src/harness/registry-reader.ts: a Harness's registry over a Chord registry value whose composition is a registry
// snapshot, read as Durable's `RegistryReader`. `snapshot()` reads the current composition (the last good one while `compose` fails),
// `subscribe` observes its compositions, its listener called synchronously inside the Chord batch that composed. `context` is the
// observation's: aborting it detaches every listener.
import type { Context } from "@earendil-works/chord/context"
import type { RegistryValue } from "@earendil-works/chord/next"
import type { RegistryReader, RegistrySnapshot, ToolRegistration } from "@earendil-works/pi-durable"

export function registryReader<Tool extends ToolRegistration = ToolRegistration>(value: RegistryValue<RegistrySnapshot<Tool>>, context: Context): RegistryReader<Tool> {
  return {
    snapshot: () => value.value,
    subscribe: listener => {
      const observation = value.observe({
        // A failed composition keeps the last snapshot: nothing to reconsider.
        advance: call => { if (call.kind === "value") listener() },
        end: () => {},
      }, context)
      return () => observation.detach()
    },
  }
}
