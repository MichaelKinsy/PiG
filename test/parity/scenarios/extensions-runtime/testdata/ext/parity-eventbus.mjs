// parity-eventbus: exercises the EventBus pub/sub (pi.events).
// Publishes a custom event on session_start, subscribes in the same
// extension to prove the bus round-trip works.
export default function (pi) {
  let received = null;

  // Subscribe to a custom topic before session_start fires.
  pi.events.on("parity-test-topic", (payload) => {
    received = payload;
  });

  pi.on("session_start", (_event, ctx) => {
    // Publish a custom event. The synchronous subscriber above captures it.
    pi.events.emit("parity-test-topic", { marker: "eventbus-ok" });

    // Surface the result via notify so the parity runner can assert it.
    if (received && received.marker === "eventbus-ok") {
      ctx.ui.notify("eventbus:round-trip:ok", "info");
    } else {
      ctx.ui.notify("eventbus:round-trip:FAIL", "error");
    }
  });
}
