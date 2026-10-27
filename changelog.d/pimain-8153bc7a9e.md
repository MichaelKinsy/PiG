### Fixed

- A tool round whose results request `Handoff` no longer ends the run on the tool-calling answer: the handoff's `pi.reset` entry is written and the same run continues with a new generation in the new context, so the submission settles with the eventual answer. Queued writes and selected steers are placed after the handoff text, follow-ups wait for the run's final boundary, and a queued reset still ends the run `unanswered` with reason `reset`. A round whose every result also sets `Terminate` ends the run after the handoff. <!-- fix: 8153bc7a9e -->
