### Added

- `deepseek-v4.1-flash` and `deepseek-v4.1-flash-1017` on the Qwen Token Plan Individual providers (`qwen-token-plan-individual`, `qwen-token-plan-individual-cn`), and a key variable per plan: `QWEN_TOKEN_PLAN_TEAM_API_KEY`, `QWEN_TOKEN_PLAN_TEAM_CN_API_KEY`, `QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY` and `QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY`. A plan's variable configures only that plan's provider; the shared `QWEN_TOKEN_PLAN_API_KEY` and `QWEN_TOKEN_PLAN_CN_API_KEY` still work for both plans of their region. <!-- feat: 1397070024 -->

### Changed

- The Qwen Token Plan providers use the current gateways (`token-plan.maas.qwencloudapi.com` international, `token-plan.maas.qianwenaiapi.com` China), and `qwen-token-plan` and `qwen-token-plan-cn` are named "Qwen Token Plan Team" and "Qwen Token Plan Team CN". Provider ids are unchanged, and a `models.json` override that names an old `aliyuncs.com` host keeps working. <!-- fix: 1397070024 -->

### Removed

- The retired `deepseek-v3.2` and `deepseek-v4-flash` from the Qwen Token Plan Team providers; use `deepseek-v4-flash-0731` or `deepseek-v4.1-flash`. <!-- fix: 1397070024 -->
