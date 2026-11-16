### Fixed

- A terminal that answers the start-up Device Attributes query at once no longer has `CSI > 4 ; 2 m` (the modifyOtherKeys fallback for a terminal without the Kitty keyboard protocol) written in the middle of PiG's start-up writes, between the cursor hide and the colour-scheme and colour queries: the reply is read once the start has written its last bytes, so the fallback follows them as in Pi. Since v0.3.0. <!-- fix: 6835163dd2 -->
