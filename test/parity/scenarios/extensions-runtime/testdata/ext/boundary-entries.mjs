// A turn_end handler appends session entries at the turn boundary (Pi agent-session.ts _commitBoundaryDrafts): the interactive mode draws them
// from their entry_appended events. BOUNDARY_KIND=entries appends a custom entry with a registered renderer, a displayed and a hidden custom
// message; BOUNDARY_KIND=compaction appends a compaction that keeps the assistant message and a displayed custom message after it.
export default function (pi) {
  pi.registerEntryRenderer("boundary-note", (entry, options, widthOrTheme) => {
    const render = () => [`boundaryentry:${entry.data}:expanded=${Boolean(options?.expanded)}`];
    if (typeof widthOrTheme === "number") return render(widthOrTheme);
    return { render, invalidate() {} };
  });
  let appended = false;
  pi.on("turn_end", (event) => {
    if (appended) return undefined;
    appended = true;
    if (process.env.BOUNDARY_KIND === "compaction") {
      return {
        entries: [
          { type: "compaction", summary: "BOUNDARY-SUMMARY", firstKeptEntryId: event.messageEntryId },
          { type: "custom_message", customType: "boundary-message", content: "AFTER-COMPACTION", display: true },
        ],
      };
    }
    return {
      entries: [
        { type: "custom", customType: "boundary-note", data: "BOUNDARY-ENTRY" },
        { type: "custom_message", customType: "boundary-message", content: "BOUNDARY-MESSAGE", display: true },
        { type: "custom_message", customType: "boundary-message", content: "BOUNDARY-HIDDEN", display: false },
      ],
    };
  });
}
