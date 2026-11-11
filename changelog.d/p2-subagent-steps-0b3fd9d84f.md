### Added

- p2's sidebar shows a subagent's current step (Thinking, Reading, Reading +2) while it works, and Failed, Stopped or the time since its newest message when it is idle, with its model, location, running tools and failure in the row's tooltip; workers report what each busy conversation does (`SessionStatus` 2, still serving 1) and subagents carry their newest message's time and last run failure (`Subagents` 3, still serving 2). <!-- feat: p2-subagent-steps -->
