### Fixed

- On Windows, the bash tool no longer risks waiting forever after a command exits while a process it started in the background still holds the output pipe. The check that asks Windows whether the command's job still has processes could read an empty answer when Go moved the goroutine stack during the call, and the tool then waited for the pipe to close. The buffers these job-object calls pass to Windows now stay in place.
