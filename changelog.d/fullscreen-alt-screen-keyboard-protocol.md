### Fixed

- Fullscreen (alternate-screen) mode re-pushes the Kitty keyboard-protocol flags when it enters the alternate screen and pops them on exit. The main and alternate screens keep independent protocol stacks, so the flags pushed for the main screen did not apply in fullscreen: `ctrl+digit` extension shortcuts stopped matching and `Shift+Enter` degraded to a bare Enter.
