### Fixed

- A LiteLLM pre-call context check (`litellm.ContextWindowExceededError: ... Context Window exceeded for given call`) is now recognised as a context overflow, so the turn compacts instead of failing, in `ai.IsContextOverflow`, the durable turn classifier and the Go, Rust, Python and Lisp extension SDKs. Carried forward from Pi d3398f82 (earendil-works/pi#11492), past the 1.4.0+b3a29f11 pin. <!-- fix: d3398f82 -->
