### Changed

- A program that embeds PiG and draws no extension mount in a terminal no longer links the terminal's image drawing or Go's JPEG and GIF decoders. The half-block drawing of a mount's image, which the terminal without an image protocol shows, now reads its pixels with the port of Pi's Photon decode (`internal/photonimage`) for every format, PNG included, instead of Go's decoders; a program that draws mount surfaces in its own terminal sets `subprocess.MountImages.Decode`, and without it such an image is one line of text that names the missing decoder and the field.
