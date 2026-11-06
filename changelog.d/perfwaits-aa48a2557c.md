### Fixed

- A command that writes to both stdout and stderr no longer keeps a reader spinning while the other stream's output is delivered, and an idle command no longer wakes its output readers every 10 ms: the bash tool and `ctx.exec` hand each stream's turn over by event. Output order is unchanged (stdout before stderr). Process CPU while a slow consumer held stdout fell from 25-34 ms to 11-14 ms in a 0.5 s run, and from 14-19 ms to 0.5 ms for two idle pipes over 2 s. <!-- fix: aa48a2557c -->
