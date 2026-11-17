// Pi 18e99051be24 (#12233): four failing synchronous listeners of one channel (one throws a string, one listener between them does not fail) and one that rejects after an await.
// The last failure of each channel carries 2 MiB of padding: the Host needs longer to read and apply that report than to route the answer written after it, so a Host that lets the answer overtake the report shows `R E` on every run, not on a lucky schedule. Pi's order does not depend on the size.
export default function (pi) {
  pi.events.on("demo:order", () => { throw new Error("first listener failed"); });
  pi.events.on("demo:order", () => {});
  pi.events.on("demo:order", () => { throw new Error("second listener failed"); });
  pi.events.on("demo:order", () => { throw "third listener failed (a thrown string)"; });
  pi.events.on("demo:order", () => { throw new Error("fourth listener failed " + "x".repeat(2 << 20)); });
  pi.events.on("demo:order-async", async () => { throw new Error("late listener failed " + "x".repeat(2 << 20)); });
}
