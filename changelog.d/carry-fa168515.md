### Fixed

- A bare URL in Markdown ends at CJK punctuation, so `https://example.com。后面的中文` links `https://example.com` and keeps the rest as text. The URL took the punctuation and the text after it into the link. Carried forward from Pi fa168515 (earendil-works/pi#10933), past the 1.4.0+b3a29f11 pin. <!-- fix: fa168515 -->
