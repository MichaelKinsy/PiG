### Fixed

- Resizing an image no longer differs from Photon's by one in a channel of a rare pixel. Photon computes the Lanczos weights with libm's single-precision `sinf`, which is not always the correctly rounded sine Go's `math.Sin` gives; PiG now runs the same `sinf`, so the resized pixels, and the PNG and JPEG written from them, are Photon's for every size measured (251 more pictures against Photon, 19 of which differed before). <!-- fix: 99d6e28a33 -->
