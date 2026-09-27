// `/long-message` sends a displayed custom message whose block content has a
// line wider than the terminal. Pi's default custom-message renderer shows it
// as Markdown wrapped inside the message box.
export default function (pi) {
  pi.registerCommand("long-message", {
    description: "Send a long custom message",
    handler: async () => {
      pi.sendMessage({
        customType: "long-probe",
        display: true,
        content: [
          { type: "text", text: "Summary **bold** line." },
          { type: "text", text: `- item: ${"word ".repeat(60).trim()} end.` },
        ],
      });
    },
  });
}
