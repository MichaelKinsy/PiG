### Fixed

- A nested `RawMessage` or `MarshalJSON` output, and an agent message or content block written by the SDK encoder, no longer lose a quote, newline, backslash or other two-character escape that stands before the letters `fffd` (`"say \"fffd\""` was written as `"say �"`): only the `\ufffd` escape itself is written as the replacement character. <!-- fix: 030f512fd6 -->
