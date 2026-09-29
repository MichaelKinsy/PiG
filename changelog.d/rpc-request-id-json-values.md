### Fixed

- RPC mode echoes a command `id` of any JSON type, including `null`, numbers, booleans, arrays, and objects, in the JavaScript form Pi's `JSON.stringify` produces, on every response and `bash_execution_update` event. An absent `id` stays absent. `id` and `type` match only exact lowercase member names.
- RPC handles an unrecognized or missing command `type` like Pi, preserving its JSON value and JavaScript coercion errors instead of treating it as a parse error. A non-null scalar or array command line produces `Unknown command: undefined`.
