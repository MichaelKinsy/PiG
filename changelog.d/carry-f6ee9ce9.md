### Fixed

- With `codemode.mode: "on"`, a tool whose output schema is a union of objects or any other multi-line type no longer puts its whole rendered output type, with its comments run together, into every request. The description now lists the field names of each variant of a union of objects (`{ action: "start", process } | { action: "list", processes, truncated? }`), shows the value of `const` fields, and points a longer type to `await describeTool("name")`. Carried forward from Pi f6ee9ce9 (earendil-works/pi#11501), past the 1.4.0+b3a29f11 pin. <!-- fix: f6ee9ce9 -->
