### Added

- A Nix flake builds PiG from source in the Nix sandbox with pinned inputs: `nix run github:MichaelKinsy/PiG`, `nix profile add github:MichaelKinsy/PiG`, or the flake's overlay. As in Pi's flake, the package puts `fd`, `ripgrep` and the clipboard commands first on `PATH` and Node.js last. Extensions build and run as in any other installation.
