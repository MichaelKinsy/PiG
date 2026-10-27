"""Run every vector case the host's type engine answers through the SDK's ``Type``, one by one, through a real host: the socket PIG_EXT_SOCKET names (the SDK dials it for each
``type.engine`` call). ``go test ./test/extension-conformance -run TestTypeEngineVectorsThroughTheHost`` serves that socket with the host's own handler and runs this script; it
prints ``ENGINE_CASES_RUN=<n>`` and exits 1 with a line per case that differs from Pi's answer."""

import os
import sys

from type_vectors_support import all_cases, check_case, needs_host


def main() -> int:
    if not os.environ.get("PIG_EXT_SOCKET"):
        print("PIG_EXT_SOCKET is not set: there is no host to run the engine cases through", file=sys.stderr)
        return 2
    cases = [case for case in all_cases() if needs_host(case)]
    failures = [failure for failure in (check_case(case) for case in cases) if failure is not None]
    for failure in failures:
        print(f"DIFF {failure}")
    print(f"ENGINE_CASES_RUN={len(cases)} FAILED={len(failures)}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
