### Changed

- The `pig` binary is 65,536 bytes smaller on Linux amd64 and 68,608 bytes smaller on Windows amd64 (61,890,720 to 61,825,184 and 63,603,712 to 63,535,104 bytes, stripped): three sorts that run once per command no longer compile their own copy of the sort. They are the drift list of `pig extension upgrade` (24,576 and 29,696 bytes), the target list of `pig piglet publish` with the keys of the project trust store (12,288 and 11,264 bytes), and the field table of the extension SDK's JSON encoder (28,672 and 27,648 bytes). Each writes the same order as before.
