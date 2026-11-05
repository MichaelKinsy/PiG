### Fixed

- Installing a chord plugin's npm dependencies no longer waits for a process the install command left running: `npm install` ended only when the command had exited and every process holding its output pipes (a daemon npm started and left behind) had let go of them, which could be for as long as that process lived. The install now ends with the command, waiting for what holds the pipes for at most five seconds after the command exited, and a command that exited with code 0 still counts as installed. <!-- fix: 31118b1520 -->
