### Fixed

- The eval harness's `submit_documentation_audit` tool declares its `verdict` as Pi's documentation audit does, a union of the three literals (`anyOf` of `{"type":"string","const":...}`), where PiG sent a string enum; the whole schema is byte for byte what Pi's TypeBox writes. <!-- fix: 23841dce31 -->
