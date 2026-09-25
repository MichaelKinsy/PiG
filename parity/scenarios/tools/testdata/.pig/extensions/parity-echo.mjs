export default function (pi) {
  pi.registerTool({
    name: "echo_bridge",
    description: "Echo back the provided text for parity bridge tests.",
    parameters: {
      type: "object",
      properties: {
        text: {
          type: "string",
          description: "Text to echo back.",
        },
      },
      required: ["text"],
    },
    async execute(_toolCallId, params) {
      return {
        content: [{ type: "text", text: `echo-bridge: ${params.text}` }],
      };
    },
  });
}
