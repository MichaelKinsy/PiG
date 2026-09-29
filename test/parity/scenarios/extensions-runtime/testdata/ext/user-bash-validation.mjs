// Pi runner.ts:isUserBashEventResult distinguishes missing, null and numeric fields.
export default function (pi) {
  pi.on("user_bash", event => {
    const result = { cancelled: false, exitCode: 0, output: "handled", truncated: false };
    switch (event.command) {
      case "undefined-exit": result.exitCode = undefined; return { result };
      case "valid": return { result };
      case "missing-exit": delete result.exitCode; return { result };
      case "null-exit": result.exitCode = null; return { result };
      case "null-path": result.fullOutputPath = null; return { result };
      case "both-with-null-operations": return { operations: null, result };
      case "empty": return {};
      case "incomplete": return { result: { output: "handled" } };
      case "throws": throw new Error("Routing failed");
      default: return undefined;
    }
  });
}
