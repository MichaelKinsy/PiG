### Fixed

- A terminal client's device pairing counted the poll interval from the start of the previous poll, so a slow poll was followed at once by the next; it now waits the whole interval after each poll was answered, as Pi does. <!-- fix: f105c622cd -->
