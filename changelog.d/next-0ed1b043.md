### Added

- PiG detects the Rex terminal (`TERM_PROGRAM=rex`) and uses OSC 8 hyperlinks and true color there (and, since Pi 396ecd7b, Kitty images; see the later entry). The check runs before the Kitty variables, so a leaked `KITTY_WINDOW_ID` does not turn images on (Pi 0ed1b043, #11131).
