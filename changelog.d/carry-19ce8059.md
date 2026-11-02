### Fixed

- The working status (OSC 7501) no longer disappears in Rex and iTerm2 during runs in regular TUI mode. Rendered messages wrote OSC 133 prompt markers to the terminal, which those terminals read as a new shell prompt; the regular-mode renderer now drops them as fullscreen mode already did. Carried forward from Pi 19ce8059 (earendil-works/pi#11519), past the 1.4.0+b3a29f11 pin. <!-- fix: 19ce8059 -->
