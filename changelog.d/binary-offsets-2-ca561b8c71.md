### Changed

- The `pig` binary is a further 16,384 bytes smaller on Linux amd64, 16,512 on macOS arm64 and 15,872 on Windows amd64 (stripped): the screen writer asks the width package the binary already holds whether a Latin, Greek or Cyrillic letter is of ambiguous East Asian width, so a second width table (`golang.org/x/text/width`) is no longer linked. The answers are the same for every letter it can ask about.
