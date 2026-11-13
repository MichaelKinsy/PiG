### Added

- The bridge of the reference web client of plugin views (`examples/ui-view-web/bridge.mjs`) takes `--theme <directory>`: the directory's `theme.css` is linked from the page and its `.css`, `.svg`, `.png`, `.woff2` and `.json` files are served under `/theme/`. A theme sets the colours the page draws a view's theme tokens with, and the page's type and frame. Without `--theme` the page is unchanged.
