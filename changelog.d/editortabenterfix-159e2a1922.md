### Fixed

- Enter on a Tab menu for an absolute path (for example after `look at /tmp/x/fi`) or on a menu of command arguments that start with `/` completes the text and keeps it in the editor; it used to send the unfinished prompt as well. Enter on a command name still runs the command, as in Pi. <!-- fix: 159e2a1922 -->
