// A Node extension that only observes: for each event it handles it reports `peer:<event>:<role>:<top>:<deep>` (the role of the message the event carries, or of the
// first user message of its list; whether the event's own probeTop member carries the edit; whether the message or messages carry it), so a row can say what a handler of
// another extension sees of an edit (TestInPlaceMessageEventEditsMatchPi). The edit's value is `other-event-edit`, which no other test uses.
export default function (pi) {
  const edited = "other-event-edit";
  const target = (event) => event.message ?? (Array.isArray(event.messages) ? (event.messages.find((m) => m?.role === "user") ?? event.messages[0]) : undefined);
  for (const name of ["message_start", "message_update", "turn_start", "turn_end", "agent_start", "agent_end", "message_end", "context"]) {
    pi.on(name, async (event, ctx) => {
      const carried = JSON.stringify(event.message ?? event.messages ?? null).includes(edited);
      ctx.ui.notify(`peer:${name}:${target(event)?.role ?? ""}:${event.probeTop === edited}:${carried}`, "info");
      // The members the pins of a top-level member look at: which declared members the event still has, and which of them carry the edit.
      const present = ["message", "messages", "assistantMessageEvent", "turnIndex"].filter((member) => member in event);
      const hasX = present.filter((member) => JSON.stringify(event[member]).includes(edited));
      ctx.ui.notify(`peerfacts:${name}:${JSON.stringify({ probeTop: event.probeTop === edited, type: event.type === edited, present, hasX })}`, "info");
    });
  }
}
