### Fixed

- When SIGINT ends interactive mode (exit status 130), PiG now ends its extension processes before it exits. The runtime process of a Node extension whose event loop was still alive, for example one that holds a timer, stayed behind after a quit by keys that was still waiting for an extension's session_shutdown handler. Nothing more is written to the session file at that exit. PiG 0.2.0 through 0.7.0 have the defect. <!-- fix: cc061a0b2, f522a9c34 -->
