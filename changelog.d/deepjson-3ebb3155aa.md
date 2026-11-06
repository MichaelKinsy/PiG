### Fixed

- Node extension runtime: a `message_update` or `tool_execution_update` handler failed with `Maximum call stack size exceeded` from about 2500 levels of nested arrays (about 4960 levels of nested objects), because the runtime copied each event payload recursively for its handler; Pi's handler gets the object. The payload is copied without recursion now, so a handler receives it up to where its own `JSON.stringify` runs out of V8 stack, as in Pi. <!-- fix: 3ebb3155aacd3c28b0e45b05ede33d45fdf9e172 -->
