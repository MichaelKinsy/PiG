### Fixed

- A terminal client's device pairing ended its wait between polls at a cancel; it now settles when the poll interval ends (at most 2 s later) with the cancellation's cause and no further poll, as Pi's bare timeout does. <!-- fix: 9e72f22abd -->
