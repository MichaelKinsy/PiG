### Fixed

- The Anthropic workload-identity token exchange now reads `expires_in` with JavaScript's `Number()`, as the Anthropic SDK does. A response that sent the lifetime as a string such as `"0x10"`, `"0b11"` or `[5]` was rejected as missing required fields although `Number()` accepts it, and one such as `"1_0"`, `"inf"` or `"0x1p3"` was accepted as a finite lifetime although `Number()` yields NaN and Pi rejects the response. <!-- fix: 1f2f94065d -->
