// The Node extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was dispatched, as a JSON string: a Node realm encodes an object it emits as a reference into its own heap. When PIG_TEST_PURE_LISTENER names a directory the listener writes the payload's tag to a file instead.
import { writeFileSync } from "node:fs";

export default function (pi) {
  const pure = process.env.PIG_TEST_PURE_LISTENER;
  pi.events.on("early:ch", data => {
    if (pure) writeFileSync(`${pure}/${data.seq}`, data.tag);
    else pi.events.emit("early:echo", JSON.stringify(data));
  });
  if (process.env.PIG_TEST_FACTORY_EMIT) pi.events.emit("early:factory", 1);
}
