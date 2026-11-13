### Fixed

- Azure OpenAI Responses requests hold the members Pi's Azure requests hold. PiG sent Azure three members Pi does not send: `prompt_cache_retention` (with long cache retention), `prompt_cache_options` (for a model with explicit cache mode) and `service_tier` (when one was configured). PiG left out two members Pi sends: `prompt_cache_key` when cache retention is `none`, and `max_output_tokens` for a model marked as not supporting it. <!-- fix: b7d2c0775e -->
