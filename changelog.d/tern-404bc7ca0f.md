### Fixed

- The system prompt a run forced no longer shows while the bash and custom messages held during that run are flushed at its end. A listener of a flushed custom message that read the system prompt saw the run's forced prompt, where Pi ends the run's prompt options first and reports the base prompt. <!-- fix: 404bc7ca0f -->
