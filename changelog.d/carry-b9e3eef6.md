### Fixed

- A redundant system prompt update (tools, rules) no longer follows an extension setting the same active tools in a different order, for example a reversed `pi.setActiveTools()` list. Tools that stay active keep their order and newly activated tools are appended. Carried forward from Pi b9e3eef6 (earendil-works/pi#11495), past the 1.4.0+b3a29f11 pin. <!-- fix: b9e3eef6 -->
