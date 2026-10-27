"""Time Type.pick through a host: the socket PIG_EXT_SOCKET names. Prints ``US_PER_CALL=<microseconds>``; run by TestTypePickRoundTripReport."""

import sys
import time

from pig_sdk import Type


def main() -> int:
    s = Type.string()
    base = Type.object({"id": s, "name": Type.optional(s), "n": Type.integer(), "tags": Type.array(s)})
    for _ in range(200):
        Type.pick(base, ["id", "name"])
    n = 5000
    start = time.perf_counter()
    for _ in range(n):
        Type.pick(base, ["id", "name"])
    print(f"US_PER_CALL={(time.perf_counter() - start) / n * 1e6:.1f}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
