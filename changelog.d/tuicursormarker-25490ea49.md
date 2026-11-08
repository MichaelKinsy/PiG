### Fixed

- A cursor marker no longer reaches the terminal when a line holds more than one or a marker sits on another visible or scrollback row, and a selection to the right of the caret no longer repeats the marker, so terminals that print the payload of an unknown APC sequence show no stray `pi:c` text. Follows Pi main 25490ea49 ahead of the pin (D300).
