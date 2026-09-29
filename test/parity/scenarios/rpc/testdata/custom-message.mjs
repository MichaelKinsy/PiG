export default function (pi) {
  pi.registerCommand("custom-message", {
    handler: () => {
      pi.sendMessage({
        customType: "probe",
        content: [{ type: "text", text: "idle-message" }],
        display: false,
        details: { marker: 42 },
      }, { triggerTurn: false });
    },
  });
}
