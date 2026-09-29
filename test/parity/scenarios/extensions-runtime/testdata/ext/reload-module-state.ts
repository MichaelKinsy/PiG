// Module-level state in a .ts extension. Pi 0.87.1 loads it through jiti with moduleCache: false, so /reload re-evaluates the module and this counter starts over.
let moduleFactoryCalls: number = 0;

export default function (pi: any) {
  moduleFactoryCalls++;
  const calls = moduleFactoryCalls;
  pi.on("session_start", (_event: unknown, ctx: any) => ctx.ui.setStatus("b-ts", `ts-module-calls=${calls}`));
}
