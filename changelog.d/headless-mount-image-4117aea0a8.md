### Changed

- A program that embeds PiG and draws no extension mount in a terminal no longer links the terminal's image drawing or Go's JPEG and GIF decoders. A program that draws mount surfaces in its own terminal calls `subprocess.EnableMountImageDecoders(jpeg.Decode, gif.Decode)` once before the first is drawn, as the `pig` command does. Without the call, a JPEG or GIF that cannot be converted to PNG is one line of text that names the missing decoder and the call.
