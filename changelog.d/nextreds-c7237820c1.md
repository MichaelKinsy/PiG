### Fixed

- In the Node extension runtime, `ctx.isIdle()` read on the statement after a triggering `pi.sendMessage(..., { triggerTurn: true })` is busy as in Pi, where the started run cannot end before the handler awaits; a run that settled while the read was in flight made print mode report an idle session. The Host now holds that answer for every SDK and extension (`isidle-2323d94ce7`), and the runtime reports only where its handler awaits. <!-- fix: c7237820c1 -->
