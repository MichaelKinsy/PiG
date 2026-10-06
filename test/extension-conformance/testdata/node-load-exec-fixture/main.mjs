// The factory runs a child with pi.exec while it loads, as Pi's loader allows (loader.ts:411-414), then reports each outcome from a command. The cases cover the options the call forwards and the rejections the host answers.
export default async function (pi) {
  const outcomes = [];
  const run = async (label, command, args, options) => {
    try {
      const result = await pi.exec(command, args, options);
      outcomes.push(`${label}=resolved:${result.code}:${result.stdout}:${result.stderr}:${result.killed}`);
    } catch (error) {
      outcomes.push(`${label}=rejected:${error.message}`);
    }
  };
  await run("ok", process.execPath, ["-e", "process.stdout.write('load-exec-ok');process.stderr.write('err')"]);
  await run("exit", process.execPath, ["-e", "process.exit(3)"]);
  await run("defaultCwd", process.execPath, ["-e", "process.stdout.write(process.cwd())"]);
  await run("cwd", process.execPath, ["-e", "process.stdout.write(process.cwd())"], { cwd: process.execPath.replace(/[\\/][^\\/]*$/, "") });
  await run("timeout", process.execPath, ["-e", "setTimeout(() => {}, 60000)"], { timeout: 200 });
  await run("empty", "", []);
  // Calls the factory does not await are in flight when it returns and settle on their own.
  const background = pi.exec(process.execPath, ["-e", "process.stdout.write('background')"]).then(result => `resolved:${result.stdout}`, error => `rejected:${error.message}`);
  pi.registerCommand("load-exec-probe", {
    description: "Report the exec calls the factory made while loading",
    handler: async (_args, ctx) => ctx.ui.notify(JSON.stringify([...outcomes, `background=${await background}`]), "info"),
  });
}
