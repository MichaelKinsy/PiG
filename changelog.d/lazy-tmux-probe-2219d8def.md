### Changed

- Inside tmux, `pig -p` no longer starts `tmux` to learn the color mode: the theme needs only true color, which the hyperlink probe does not decide. A `pig -p` turn in tmux took 9.2 ms and now takes 7.0 ms.
