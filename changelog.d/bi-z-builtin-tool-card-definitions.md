### Fixed

- A model's `bash`, `powershell`, `grep`, `find` or `ls` call is now drawn by the tool's built-in card, as Pi does and as `read`, `write` and `edit` already were. The interactive mode took the plain card for those calls unless an extension registered the tool, so a collapsed bash preview at a narrow width wrapped its "earlier lines" hint with other colour resets, an image in the result drew nothing, and the elapsed time past a minute stayed in seconds.
