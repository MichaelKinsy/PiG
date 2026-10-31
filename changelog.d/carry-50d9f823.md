### Fixed

- Tool output bounded by `chord/output`, the durable core and the Cloudflare host now loses whole ANSI escape sequences (strings up to BEL or ST, CSI, short `ESC x` forms), not only their ESC bytes, which left `[31m` in transcripts; a sequence split across chunks goes too. Carried forward from Pi 50d9f823 (earendil-works/pi#11604), past the 1.4.0+b3a29f11 pin. <!-- fix: 50d9f823 -->
