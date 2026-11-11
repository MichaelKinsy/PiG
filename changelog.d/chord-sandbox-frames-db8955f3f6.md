### Fixed

- A terminal sandbox chunk holding valid frames before a malformed one delivered the valid messages before failing; it now delivers none of that chunk's messages, as Pi does. <!-- fix: db8955f3f6 -->
