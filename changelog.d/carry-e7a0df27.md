### Fixed

- Resizing an image sends the smaller of the PNG and the JPEG at the configured quality instead of the PNG whenever it fit, so a photo that needs resizing no longer goes out as a multi-MB PNG that makes the request fail or time out. Screenshots where the PNG is smaller still go out as PNG, and the lower JPEG qualities are tried only when neither fits. Carried forward from Pi e7a0df27 (earendil-works/pi#11551), past the 1.4.0+b3a29f11 pin. <!-- fix: e7a0df27 -->
