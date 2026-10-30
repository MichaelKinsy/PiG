### Fixed

- The durable `read` tool prepares an image with a width or a height of zero (a farbfeld file with no pixels, an interlaced GIF with no rows) as Pi does: under a type providers do not take inline, or beyond a limit, the image is resized to at least one pixel and encoded, with the resize recorded from the empty size, instead of being left out. A resize that would need the PNG of the empty image itself fails as it does in Pi.
