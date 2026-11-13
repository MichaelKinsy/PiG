### Fixed

- A Node extension start no longer loads pi-tui's text-width module; it loads when an image line is first measured. Cause: the Node runtime loads pi-tui's terminal-image module at every start (the runtime hands it the host's terminal capabilities, and the theme's colour mode reads them from it), and the backport of Pi 787005a40 made that module import the width module for its image-line extent, so the merge that brought the backport loaded the width tables at every start. No released PiG had this: the import arrives with that merge. <!-- fix: 6f949d9716 -->
