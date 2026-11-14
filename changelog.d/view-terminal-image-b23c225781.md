### Added

- A view that an extension mounts can show a photograph in the terminal. The terminal view client draws the `image` node of a mount (PNG, JPEG, GIF or WebP) in a panel. In kitty and Ghostty it is a real image that is sent to the terminal once and then moves, scrolls and disappears with the view. In other terminals with the Kitty graphics protocol and in iTerm2 it is an inline image. In a terminal without an image protocol, tmux included, it is drawn with half-block characters in the terminal's colours; an image that cannot be decoded is one line of text that names it. The image is prepared in the background, so the interface stays responsive while a large photograph loads. `maxWidthCells` and `maxHeightCells` size it, and it is never taller than its slot.

### Changed

- An image in a mounted view counts against a client's snapshot size and no longer against its text limit. Before, every client that takes 65,536 bytes of text refused an image of more than 49,152 bytes. The terminal takes an image of about 195,000 bytes; the web client's snapshot limit is 1,048,576 bytes.
