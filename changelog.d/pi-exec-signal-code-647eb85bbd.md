### Fixed

- `pi.exec` no longer reports code 0 for a command that a signal ended. A command that timed out, was aborted, crashed or was killed from outside returned `code: 0`, which an extension checking `result.code === 0` took for a success; it now returns 128 plus the signal number as a shell does (143 for SIGTERM, 137 for SIGKILL), as Pi does (upstream #12089). <!-- fix: 647eb85bbd8cb825b0fec331f20b20abcdeab41a -->
