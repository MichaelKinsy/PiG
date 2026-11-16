package subprocess

const throwerExtension = `export default function (pi) {
  pi.on("tool_call", (event) => {
    switch (event.input.mode) {
      case "string": throw "nope";
      case "error": throw new Error("boom");
      case "empty": throw new Error("");
      case "message-object": throw { message: "not an Error" };
      case "undefined": throw undefined;
      case "number": throw 7;
    }
  });
  pi.registerTool({
    name: "thrower",
    description: "throws",
    parameters: { type: "object", properties: { mode: { type: "string" } } },
    async execute(_id, params) {
      if (params.mode === "empty") throw new Error("");
      throw { message: "not an Error" };
    },
  });
}
`
