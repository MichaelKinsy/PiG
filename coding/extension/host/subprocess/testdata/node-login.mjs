const definition = {
  brand: Array(5).fill("A".repeat(41)),
  hero: Array(14).fill("A".repeat(32)),
  mascot: Array(14).fill("A".repeat(16)),
  palette: { A: "#112233" },
  name: "Node Login",
  description: "runtime adapter",
  tagline: "one host call",
};

export default function (pi) {
  pi.registerCommand("set_node_login", {
    description: "exercise the Node login adapter",
    handler: async (_args, ctx) => {
      await ctx.ui.setLogin(definition);
    },
  });
  pi.registerCommand("set_invalid_node_login", {
    description: "surface a rejected Node login",
    handler: async (_args, ctx) => {
      await ctx.ui.setLogin({ ...definition, brand: ["short"] });
    },
  });
}
