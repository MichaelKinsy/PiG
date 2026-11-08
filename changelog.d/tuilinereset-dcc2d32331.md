### Changed

- The main screen's per-line reset costs less: a line is checked for images and cursor markers in one pass and the marker strip no longer adds a scan or an allocation, so a styled row and a row with a stray marker are each one allocation and faster than before the cursor-marker strip, with the same output.
