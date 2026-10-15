// Records the member order of each turn_end context entry's sourceEntry and of its message.
// Pi hands turn_end handlers the session's own entry objects (session-manager.ts buildSessionProjection), so their members are in the order the session wrote them (agent-session.ts _buildBoundaryContext).
// Members whose value is undefined do not cross the extension process boundary as JSON, so they are left out of the record.
import { appendFileSync } from "node:fs";

const keys = (value) => Object.keys(value).filter((key) => value[key] !== undefined).join(",");

export default function (pi) {
  pi.on("turn_end", (event) => {
    const lines = event.context.contextEntries.map((entry) => {
      const source = entry.sourceEntry;
      return `${keys(source)} | ${source.message ? keys(source.message) : ""}`;
    });
    appendFileSync(process.env.BOUNDARY_ORDER_LOG, `${lines.join("\n")}\n--\n`);
  });
}
