### Fixed

- Go embedding hosts can bind extension appends through `coding.Session.AppendExtensionEntry`, retaining subprocess IDs and timestamps through the native Session writer and preserving `entry_appended` semantics. Previously, calling `AppendCustomEntry` allocated a new identity instead of retaining the subprocess ID and timestamp, leaving duplicate entries in the Node session mirror. <!-- fix: bf453203b656bcbb1aa0a0c6f70fa9cc1562ac5e -->
