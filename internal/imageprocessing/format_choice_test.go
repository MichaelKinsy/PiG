package imageprocessing

import "strings"

// Pi picks the smaller of the PNG and the JPEG candidate of a resize (image-resize-core.ts, e7a0df27, #11551). Which one is smaller depends on
// the pixels the resampler produced and on the byte counts of the encoders, and both differ between Photon (Lanczos3, the image crate's
// encoders) and PiG (x/image CatmullRom, the standard library's encoders): for a downscaled test picture PiG's PNG is up to twenty times
// the size of Photon's (7,390 against 312 bytes for a 1739x2001 picture of sparse samples scaled to 50x58). The Pi oracles therefore
// compare the format of a resized image only when it is not one of those two: the choice itself is pinned by
// TestFirstFittingCandidatePrefersTheSmallerOfPNGAndJPEG and TestResizeImagePicksJPEGOverLargerPNGForPhotos, whose pictures leave no doubt.

// pngOrJPEG reports whether both formats are PNG or JPEG, the pair a resize chooses between.
func pngOrJPEG(a, b string) bool {
	is := func(mime string) bool { return mime == "image/png" || mime == "image/jpeg" }
	return is(a) && is(b)
}

// hintsWithoutFormat is a conversion hint with the format the image ended up in (which the PNG/JPEG choice decides) left out.
func hintsWithoutFormat(hints string) string {
	return strings.NewReplacer("image/jpeg", "image/*", "image/png", "image/*").Replace(hints)
}
