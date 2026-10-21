### Fixed

- A user_bash `operations` object from an extension is no longer released while a command is running through it. The extension's handle is released when the host drops the object, but the running exec did not keep the object reachable, so a garbage collection mid-command could delete the extension's table entry under the command. <!-- fix: 6d3cfc863d52939a998bc61b2183ebac58e5d12f -->
