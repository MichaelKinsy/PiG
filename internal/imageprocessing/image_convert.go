package imageprocessing

// ConvertImageBytesToPng applies EXIF orientation before encoding PNG, as
// upstream image-convert.ts does through Photon. Failure returns nil.
func ConvertImageBytesToPng(data []byte) []byte {
	img, err := decodeAutoOriented(data)
	if err != nil {
		return nil
	}
	png, err := encodePNG(img)
	if err != nil {
		return nil
	}
	return png
}
