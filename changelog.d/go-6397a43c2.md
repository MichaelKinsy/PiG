### Security

- Updated the pinned Go toolchain from 1.27.3 to 1.27.4, which fixes GO-2026-6859 (a crash from a data race on the response header map when `net/http.MaxBytesReader` is used) and GO-2026-6848 through GO-2026-6858 (cgo compiler and linker flag injection). PiG reaches the `net/http` flaw through its HTTP clients and servers, so `govulncheck` failed on every build with the previous toolchain.
