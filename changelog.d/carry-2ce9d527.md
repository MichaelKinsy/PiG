### Fixed

- Wrapped text no longer starts the next line with a space when a styled word ends exactly at the wrap width, and strikethrough no longer runs into the right padding of a wrapped line: the wrap closes `\x1b[29m` at line end like underline and opens it again on the next line. Carried forward from Pi 2ce9d527 (earendil-works/pi#10925), past the 1.4.0+b3a29f11 pin. <!-- fix: 2ce9d527 -->
