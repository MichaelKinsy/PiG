// Shared factory for the published-Pi and real Node-subprocess Session boundary probe.
export default function (pi) {
  let requested = false;
  pi.on("turn_end", event => {
    if (requested) return;
    requested = true;
    if (!event.messageEntryId || event.outcome !== "completed") throw new Error("missing turn metadata");
    event.entries.push({type:"custom_message", customType:"bridge-boundary", content:"context from real extension", display:false, details:{source:"boundary-probe"}});
    throw new Error("retained boundary error");
  });
  pi.on("turn_end", event => {
    if (event.entries.length === 0) return;
    if (!event.context.contextMessages.some(message => message.customType === "bridge-boundary" && message.content === "context from real extension")) throw new Error("missing updated boundary preview");
    return {continue:true};
  });
}
