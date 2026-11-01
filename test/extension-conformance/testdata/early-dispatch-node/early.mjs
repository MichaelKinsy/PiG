// The Node extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was dispatched, as a JSON string: a Node realm encodes an object it emits as a reference into its own heap.
export default function (pi) {
  pi.events.on("early:ch", data => { pi.events.emit("early:echo", JSON.stringify(data)); });
}
