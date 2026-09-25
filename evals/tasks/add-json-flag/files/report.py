"""Print a word-count report for the files named on the command line."""
import sys
from collections import Counter


def counts(paths):
    total = Counter()
    for path in paths:
        with open(path) as f:
            total.update(f.read().split())
    return total


def main(argv):
    for word, n in sorted(counts(argv).items()):
        print(f"{word}: {n}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
