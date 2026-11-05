### Fixed

- The OpenAI Responses API now gets the session-affinity header `session-id` instead of `session_id`, as in Pi, because nginx drops underscored headers by default and Envoy can reject them, which lost cache affinity behind such proxies. The Completions API still sends `session_id`. <!-- fix: bf094afb3e -->
