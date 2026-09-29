### Fixed

- Startup reports extension, model, and API-key diagnostics together in Pi's order. Model-scope warnings also appear for `--help` and `--list-models`. Invalid `@file` arguments are reported before runtime diagnostics.
- JSON mode without an authenticated model emits the Session header. Empty print and JSON invocations succeed, while an unhandled prompt reports Pi's authentication guidance after extension command and input handlers run.
- RPC reports JavaScript coercion errors for non-string command types whose objects shadow `toString`, including nested array elements.
