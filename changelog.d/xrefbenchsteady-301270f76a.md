### Fixed

- An extension that emits an event to listeners in another extension process no longer pays a host round trip for a varying share of its emits. The listener process now reports that it has run its listeners before it sends its answer, so the host settles the emit when the answer arrives instead of depending on whether a second read has already delivered the report; the same 200 emits cost the same frames on every run. <!-- fix: 301270f76a -->
