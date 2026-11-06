// An extension that answers every compaction with an empty summary (session_before_compact result, agent-session.ts:2900).
export default function (pi) {
  pi.on("session_before_compact", async (event) => ({
    compaction: {
      summary: "",
      firstKeptEntryId: event.preparation.firstKeptEntryId,
      tokensBefore: event.preparation.tokensBefore,
    },
  }));
}
