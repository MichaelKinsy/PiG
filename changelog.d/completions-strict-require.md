### Fixed

- A tool with JSON-schema constrained sampling `strict: "require"` now fails an OpenAI Chat Completions request with `Tool "<name>" requires JSON-schema constrained sampling, but strict tools are unsupported.` when the model's compat does not enable strict mode, as under Pi. It was sent as an ordinary function tool instead. `prefer` is unchanged.
