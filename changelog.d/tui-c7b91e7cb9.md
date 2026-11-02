### Fixed

- Terminal input bytes decode as Node's StringDecoder does, as Pi's `stdin.setEncoding("utf8")` reads them: a trailing C0, C1, F5-F7, or an E0, ED, F0 or F4 lead followed by an out-of-range byte is held until the next read instead of becoming U+FFFD at once, so a mouse report ending in such a byte is no longer taken as a wheel scroll. <!-- fix: c7b91e7cb9ac4939eeef016dd25ca4b009a2c022 -->
