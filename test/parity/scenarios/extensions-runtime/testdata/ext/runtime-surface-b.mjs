// runtime-surface-b: a second extension on the same pi.events bus.
export default function (pi) {
  pi.events.on("surface:ping", (data) => console.error(`surface-b: heard ${data.n}`));
}
