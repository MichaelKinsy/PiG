### Fixed

- Provider error text is written the way Pi writes it. A JSON error body keeps U+2028 and U+2029 as they are instead of `\u2028`, writes `-0` as `0`, keeps lone surrogates, and lists integer-like keys first (`JSON.stringify` rules). Text that is cut to a length limit (error bodies, Radius gateway text, Pi message text) keeps a replacement character where the cut falls inside an emoji or other pair of UTF-16 units, as Pi's `slice` leaves half of the pair, instead of dropping the half.
