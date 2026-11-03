### Fixed

- A `message_end` handler that returns a system message with missing or null content now keeps that content, as Pi does, instead of getting an empty string. <!-- fix: 5c1e7a90d3 -->
