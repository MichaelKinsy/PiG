### Fixed

- `Shift+Enter` inserts a newline instead of queueing a follow-up in Apple Terminal setups that send `ESC` + Return. Where native Shift detection is available (Apple Terminal, native Windows), `ESC` + Return with Shift held is `Shift+Enter`; without Shift it stays `Alt+Enter`. Carried forward from Pi 86dc686c (earendil-works/pi#11559), past the 1.4.0+b3a29f11 pin. <!-- fix: 86dc686c -->
