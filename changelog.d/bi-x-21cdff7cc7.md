### Fixed

- A row that fills the terminal and holds an unpaired UTF-16 surrogate, for example text cut inside an emoji, no longer stops the interface with "Rendered line N exceeds terminal width". The surrogate is measured as zero cells and written to the terminal as U+FFFD when the frame is written, as in Pi, instead of being widened to U+FFFD before the row was measured. <!-- fix: 21cdff7cc7 -->
