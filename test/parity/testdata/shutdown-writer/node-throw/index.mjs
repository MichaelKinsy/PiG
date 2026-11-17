// shutdown-writer/node-throw: the session_shutdown handler appends an entry and then throws; Pi reports the error through the extension runner (stderr) and writes nothing to the JSON stream.
export default function (pi) {
  pi.on("session_shutdown", () => {
    pi.appendEntry("shutdown-entry", { text: "from-shutdown" });
    throw new Error("shutdown-boom");
  });
}
