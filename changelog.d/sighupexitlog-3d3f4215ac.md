### Fixed

- A hung-up terminal now leaves exactly one `exit.log` line, `received SIGHUP`. The kernel's SIGHUP, the input reader's EOF and a failed terminal write each recorded an ending of their own and raced `os.Exit`, so a hangup wrote three lines or two lines without the signal's, never the one line D102 promises. The first path now owns the record, a dead-terminal exit waits for a SIGHUP that is on its way to claim it, and the line is flushed before the process exits. The exit code (129), the terminal restore and the output are unchanged (D102). <!-- fix: 3d3f4215ac -->
