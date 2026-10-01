### Fixed

- Fullscreen (alternate-screen) mode keeps the Kitty keyboard-protocol flags on the active screen, as Pi does. The main and alternate screens keep independent protocol stacks, so the flags pushed for the main screen did not apply in fullscreen: `Shift+Enter` degraded to a bare Enter and `ctrl+digit` extension shortcuts stopped matching. PiG also no longer leaves the Kitty keyboard flags set in the shell after it exits fullscreen, where `ctrl+c` arrived as an escape sequence instead of `^C`. Thanks @jkerdreux-imt.
