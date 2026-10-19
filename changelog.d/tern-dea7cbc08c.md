### Fixed

- A `--print` or `--mode json` run that SIGTERM or SIGHUP reaches while its bash tool is starting the shell no longer has a data race on `os.Stdout`. Starting a child process (a shell, an extension command, a Durable child) decided which of the child's files to leave open by reading the `os.Stdout` variable, which the exit path's restore of the output guard assigns at the same time. It now compares them with the files `os.Stdin`, `os.Stdout` and `os.Stderr` held when pig started, which stay open as before. The output and exit status are unchanged.
