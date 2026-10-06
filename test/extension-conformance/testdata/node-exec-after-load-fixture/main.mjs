// The factory makes no host call, so it opens no connection while it loads, and returns. The callback it scheduled runs on a later turn of the event loop, while the runtime opens the connection it registers on, and calls pi.exec there. Pi's exec spawns the child whenever it is called (loader.ts:411-414), in the loader's working directory.
export default function (pi) {
  let afterLoad = Promise.resolve("not-called");
  setImmediate(() => {
    afterLoad = pi.exec(process.execPath, ["-e", "process.stdout.write('after-load:' + process.cwd())"]).then(result => `resolved:${result.code}:${result.stdout}`, error => `rejected:${error.message}`);
  });
  pi.registerCommand("exec-after-load-probe", {
    description: "Report the exec call a callback made after the factory returned",
    handler: async (_args, ctx) => ctx.ui.notify(await afterLoad, "info"),
  });
}
