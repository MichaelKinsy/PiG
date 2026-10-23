### Fixed

- Extensions' `ui_prompt_start` and `ui_prompt_end` notifications are recorded in order when the host is slow to apply the start event's `ctx.ui.notify`: the dialog's answer is no longer let through, and the end event no longer sent, before the handler's own host call has applied. A blocked-on-a-host-call report from an extension runtime admitted the UI prompt drain as soon as it was read, while the call it announced was still queued; the admission now waits for that call. <!-- fix: f0b910f8ca -->
