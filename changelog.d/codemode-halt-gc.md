### Fixed

- A code mode script stuck in a loop that never reaches the JavaScript engine's interrupt check (a native string or regular-expression loop, or a custom WebAssembly module) can no longer freeze the whole process: Go could not stop the world for a garbage collection while the loop ran, so no timer fired and the script's hard stop was never delivered, which hung small machines (2 CPUs or fewer) for minutes. Compiled loops now reach Go every 1024 iterations, as Pi's worker `terminate()` reaches a runaway script, and the hard stop runs on time.
