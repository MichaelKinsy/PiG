### Fixed

- The Durable core conformance gate runs `U-codemode` and `U-harness-tools` alone, their reference run before their candidate run. Their upstream tests assert inside a real-time window that the run fills itself (`times a script out across a restart` gives a script 300 ms to start the sandbox and reach a tool, 148-207 ms of CPU); next to the other rows, the host starved either run, and in six full runs at `-j 8` the candidate missed the window four times and the reference once. <!-- fix: 78f1007d3e1ade8c0515032e3e9068514a51ffa1 -->
