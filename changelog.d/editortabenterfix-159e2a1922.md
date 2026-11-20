### Fixed

- Enter on a Tab menu for an absolute path (`look at /tmp/x/fi`) or on a command argument menu whose items start with `/` (`/open /tm`) completes the text and keeps it in the editor; it used to send the unfinished prompt as well. Enter on a command name (`/mo`) still runs the command, as in Pi. <!-- fix: 159e2a1922 -->
