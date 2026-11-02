### Fixed

- A terminal read that ends inside a UTF-8 character and holds every byte of it no longer restarts the ESC timer, as in Pi, where Node emits no data event for it. ESC, then the two bytes of é arriving 5 ms and 11 ms later, is Escape followed by é, not Alt+é. <!-- fix: 3c2dde41465506033cf266e06e988c6bfa8f7f3f -->
