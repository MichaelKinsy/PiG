### Changed

- Chord: a reload restarts every live object of a changed plugin and the calls running at the commit (and every call they cause, over trusted links too) finish on the old code; new calls wait for the new code. A wait that would cycle through its own drain fails at once with `unavailable/reload_cycle`. A call's answer now follows its state over a link. <!-- fix: d892104fad -->
