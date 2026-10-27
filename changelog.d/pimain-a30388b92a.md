### Fixed

- GPT-5.4 and GPT-5.5 no longer fail with `Missing namespace for function_call` after switching from another model that called tools. On the OpenAI and OpenCode providers they declare their tools in the top-level tool list instead of loading them through `additional_tools` or tool search; GPT-5.6 and later keep `additional_tools`. Ports Pi's #11415. <!-- fix: a30388b92a -->
