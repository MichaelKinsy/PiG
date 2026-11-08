### Fixed

- A Pi extension that starts the harness again (`process.execPath process.argv[1] ...`) and ends it with SIGKILL after SIGTERM no longer leaves the PiG binary running: the harness entry cannot forward SIGKILL, so a small watchdog it starts kills the binary when the entry dies. Signal forwarding, stdio and exit status are unchanged. Windows ends the binary by pid alone and is untested. <!-- fix: 59c181494e -->
