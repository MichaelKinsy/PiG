### Fixed

- On Windows, an extension's `exec` no longer returns no output for a command that printed, when the machine is too busy to run PiG's pipe reader within 100 ms of the command's exit. The command now starts suspended and joins a job object before it can start another process, as the bash tool's shell already did, so PiG knows when no process holds the output pipes any more and reads them to the end instead of dropping what the command had written before it exited. <!-- fix: 8f88d43fa5e3a208a8294343f2e444811df19055 -->
