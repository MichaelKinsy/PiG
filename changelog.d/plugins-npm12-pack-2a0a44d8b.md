### Fixed

- Installing a Chord plugin works with npm 12: `npm pack --json` prints an object keyed by package name there, and the install now reads the member of the package it asked for instead of the first member (the list npm 11 and earlier print is read as before), as in Pi. <!-- fix: 2a0a44d8b02e0641c82373a6311a1fa754cf7585 -->
