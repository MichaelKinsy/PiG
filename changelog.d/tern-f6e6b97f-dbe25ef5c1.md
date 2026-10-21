### Fixed

- Fixed `pig --mode rpc` exiting 143 instead of 0 when stdin ended while a termination signal's shutdown was still running: the first exit of the shutdown now decides the exit status, as Pi's first `process.exit` does. <!-- fix: dbe25ef5c1 -->
