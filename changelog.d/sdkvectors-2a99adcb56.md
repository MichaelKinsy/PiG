### Changed

- The Python, Rust and Lisp SDK tui helpers slice a line like Pi: an APC sequence before the slice (a cursor marker) is not carried into it. All four SDKs detect Alacritty by `TERM=alacritty` or `ALACRITTY_WINDOW_ID` as Pi does, and their Pi vectors are regenerated at the pin.
