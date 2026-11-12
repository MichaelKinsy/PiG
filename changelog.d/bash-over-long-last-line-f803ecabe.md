### Fixed

- The bash tool no longer returns no output, with a footer like `[Showing lines 3-2 of 2]`, when a command's output ends in one line longer than about 100KB, such as a `grep` match in a minified file; it shows the end of that line, as Pi does (upstream #12092). <!-- fix: f803ecabe155f051718d2cad2942bce2daad802c -->
