export default function register(pi) {
  pi.on("project_trust", () => ({ trusted: "undecided" }));
  pi.on("project_trust", () => ({ trusted: "yes", remember: true }));
  pi.on("project_trust", () => {
    throw new Error("must-not-run-after-decision");
  });
}
