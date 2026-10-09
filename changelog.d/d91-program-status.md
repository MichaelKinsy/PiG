### Added

- A frontend member's session now receives PiG's program status (D91): working, blocked while a dialog or a login waits, then done, error or idle, as the OSC 7501 report Pi 1.1.0 writes to a supporting terminal. A session that implements `frontend.ProgramStatusSession` receives every change, including the clear before `Suspend` and `Close`. While a session may draw, PiG no longer writes the OSC 7501 support query or a report to the terminal, where they bypassed the member. Stock PiG and a Piglet without a frontend member report the status as before.
