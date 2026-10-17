"""A vendored third-party dependency of the robust-notes fixture: POSIX-style word splitting with quotes."""


def split(line):
    words = []
    word = ""
    in_word = False
    quote = ""
    for ch in line:
        if quote:
            if ch == quote:
                quote = ""
            else:
                word += ch
        elif ch in ("'", '"'):
            quote = ch
            in_word = True
        elif ch in (" ", "\t"):
            if in_word:
                words.append(word)
            word = ""
            in_word = False
        else:
            word += ch
            in_word = True
    if quote:
        raise ValueError("unterminated quote")
    if in_word:
        words.append(word)
    return words
