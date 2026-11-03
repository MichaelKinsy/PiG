### Fixed

- A run whose model answer holds a value that is not strict JSON, a function or a non-finite number (`NaN`, `Infinity`), now faults with Pi's text, `Value contains a non-JSON function; expected strict JSON` or `Value contains a non-finite number and is not strict JSON`, in the submission's `detail` and in `pi.live.runFailure.detail`. It used to report Go's `json: ... unsupported type: func()` or `unsupported value: NaN`. <!-- fix: bf89558136, de985c5719 -->
