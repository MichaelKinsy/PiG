### Fixed

- Chord: a reload whose new factory pins a keyed member of another object (`ref.keyed.at(key)`) no longer fails the whole plugin with "is valid only in the factory". The recording self a reload runs the factory on handed out references with no dependent. <!-- fix: a79074ec58 -->
