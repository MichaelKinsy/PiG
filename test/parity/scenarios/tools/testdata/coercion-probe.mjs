import { Type } from "typebox";

export default function (pi) {
  pi.registerTool({
    name: "coercion_probe",
    description: "Report converted native TypeBox and plain JSON arguments",
    parameters: Type.Object({
      native: Type.Object({
        integer: Type.Integer(), flag: Type.Boolean(), array: Type.Array(Type.Integer()),
        optional: Type.Optional(Type.Number()),
      }),
      plain: {
        type: "object", required: ["integer", "flag"],
        properties: { integer: { type: "integer" }, flag: { type: "boolean" }, optional: { type: "number" } },
      },
    }),
    async execute(_id, args) {
      const { native, plain } = args;
      const observed = [native.integer, native.flag, native.array, Object.hasOwn(native, "optional"), plain.integer, plain.flag, Object.hasOwn(plain, "optional")];
      return { content: [{ type: "text", text: JSON.stringify(observed) }] };
    },
  });
}
