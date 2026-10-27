### Fixed

- A `ChordError` of code `invalid` from a failed args, result, state or input check carries TypeBox's whole error objects as `details` (keyword, schemaPath, instancePath, params and message), as Pi's does; it carried only instancePath and message. <!-- fix: 7f2c05b082 -->
