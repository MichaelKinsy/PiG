### Added

- Extension-registered models (`pi.registerProvider`) also carry `samplingParamsByThinkingLevel`, as in Pi 1.0.2, so their per-level sampling parameters reach OpenAI-compatible requests.

### Fixed

- A Durable `Submission.Wait` that started while the harness was closing could block forever; it now fails with `Harness is closed`.
