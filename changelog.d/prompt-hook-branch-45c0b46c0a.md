### Fixed

- Interactive mode: a `before_agent_start` handler's edit of `systemPromptOptions` (for example `appendSystemPrompt`, `customPrompt` or `contextFiles`) is sent to the model also when a skill is loaded and the active tools do not include `read`. In that case the request kept the startup prompt and changed only its tool list and rules, so the edit was lost and the skills section still told the model to use the `read` tool. Print, JSON and RPC mode were not affected. <!-- fix: 45c0b46c0a40582190d677af1cebb573e1e9214a -->
