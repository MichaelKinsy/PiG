export default function (pi) {
  pi.registerCommand("exec_probe", {
    description: "call pi.exec and report the result",
    handler: async () => {
      // PIG_TEST_EXEC_HELPER_PATH points at a portable, natively executable
      // helper (the test binary itself, run in "echo" mode) instead of an
      // external /bin/echo, so this fixture also works on native Windows.
      const helper = process.env.PIG_TEST_EXEC_HELPER_PATH;
      if (!helper) throw new Error("PIG_TEST_EXEC_HELPER_PATH not set");
      const result = await pi.exec(helper, ["exec-probe-ok"]);
      if (result.code !== 0) throw new Error(`exec code = ${result.code}, stderr = ${result.stderr}`);
      if (result.stdout.trim() !== "exec-probe-ok") {
        throw new Error(`exec stdout = ${JSON.stringify(result.stdout)}`);
      }
    },
  });
  // Runs a command that never ends on its own and aborts its signal: pi.exec kills the command and resolves with killed set and code 0, in every mode.
  pi.registerCommand("exec_abort_probe", {
    description: "abort the signal of a running pi.exec",
    handler: async () => {
      const helper = process.env.PIG_TEST_EXEC_HELPER_PATH;
      if (!helper) throw new Error("PIG_TEST_EXEC_HELPER_PATH not set");
      const controller = new AbortController();
      setTimeout(() => controller.abort(), 500);
      const result = await pi.exec(helper, ["hang"], { signal: controller.signal });
      if (!result.killed || result.code !== 0) throw new Error(`exec killed = ${result.killed}, code = ${result.code}, want killed with code 0`);
    },
  });
}
