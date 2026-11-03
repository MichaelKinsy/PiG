### Fixed

- A run whose model answer holds a function now faults with the text `Value contains a non-JSON function; expected strict JSON`, as in Pi, in the submission's `detail` and in `pi.live.runFailure.detail`. It used to report Go's `json: unsupported type: func()`. <!-- fix: bf89558136 -->
