### Changed

- The built-in durable tools `read`, `write`, `edit`, `bash` and `powershell` declare their parameters in the member order of pi-schema's builders (`type`, `properties`, `required`, objects open) instead of TypeBox's (`type`, `required`, `properties`), as Pi does since 6536f801; the read tool's structured output schema names the image block's `type` as a bare `{"const":"image"}`.
