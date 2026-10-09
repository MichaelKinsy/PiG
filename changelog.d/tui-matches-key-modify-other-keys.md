### Fixed

- Key matching follows Pi for three terminal inputs. A modified `escape` (`ctrl+escape`, `shift+alt+esc`) matches no key binding, as in Pi, although the same input still parses to that name. An xterm modifyOtherKeys sequence (`ESC [ 27 ; mod ; code ~`) matches only when its modifier is exactly the binding's, so a Caps Lock or Num Lock bit no longer matches. An unmodified letter, digit or symbol binding no longer matches a modifyOtherKeys sequence.
