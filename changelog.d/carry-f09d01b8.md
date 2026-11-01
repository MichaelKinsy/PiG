### Fixed

- Image components render inline in Orca terminals that advertise Kitty graphics (`TERM_PROGRAM=Orca` with `ORCA_IMAGE_PROTOCOL=kitty`) instead of falling back to text. Orca releases that do not advertise it keep the text fallback. Carried forward from Pi f09d01b8 (earendil-works/pi#9329), past the 1.4.0+b3a29f11 pin. <!-- fix: f09d01b8 -->
