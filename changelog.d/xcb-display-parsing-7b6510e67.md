### Fixed

- On Linux, the native X11 clipboard reads a `DISPLAY` that starts with `/` or `unix:` as a socket path with an optional screen (`/tmp/.X11-unix/X0.1`, `unix:/tmp/.X11-unix/X0`), as libxcb 1.16 and later do. A socket path now connects where it failed before, and `DISPLAY=unix:0` no longer names display 0. Pi's addon links the host's libxcb, so hosts with libxcb 1.15 or earlier read these displays differently. <!-- fix: 7b6510e67 -->
