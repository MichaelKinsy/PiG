### Fixed

- RPC mode starts the `--tools` list active in its own order when it names extension tools, as Pi does. `--tools grep,my_tool,read` declares `grep`, `my_tool`, `read` to the model and lists them in that order in the system prompt; RPC mode listed the built-in tools first and appended the extension tools.
