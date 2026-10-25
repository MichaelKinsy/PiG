# SPDX-License-Identifier: MIT
#
# Acceptance checks of the package, run in the Nix sandbox with no network. `nix flake check` builds them.
{ pkgs, pig }:

let
  inherit (pkgs)
    lib
    stdenv
    runCommand
    closureInfo
    python3
    rustPlatform
    libxcb
    ;
  fixtures = ./tests/extensions;
  driver = ./tests/acceptance.py;

  # The crates the Rust SDK depends on, vendored from its own lockfile, so Rust extensions build offline in the sandbox. A user's Rust
  # extension builds fetch them from crates.io as usual.
  cargoHome = runCommand "pig-check-cargo-home" { } ''
    mkdir -p $out
    cat > $out/config.toml <<EOF
    [source.crates-io]
    replace-with = "vendored-sources"

    [source.vendored-sources]
    directory = "${rustPlatform.importCargoLock { lockFile = ../extensions/sdk-rs/Cargo.lock; }}"

    [net]
    offline = true
    EOF
  '';

  acceptance =
    name: package: flags:
    runCommand name
      {
        nativeBuildInputs = [ python3 ];
        CARGO_HOME = cargoHome;
        GOPROXY = "off";
      }
      ''
        mkdir -p "$TMPDIR/work"
        python3 ${driver} --pig ${lib.getExe package} --installed ${package}/share/pig/sdk \
          --fixtures ${fixtures} --work "$TMPDIR/work" ${flags}
        touch $out
      '';

  # An installation whose Rust SDK is missing and whose Python SDK marker names another SDK.
  broken = pig.overrideAttrs (previous: {
    postInstall = previous.postInstall + ''
      rm -r $out/share/pig/sdk/sdk-rs-*
      echo 0000000000000000 > $out/share/pig/sdk/sdk-py-*/.pig-sdk-version
    '';
    doInstallCheck = false;
  });
in
{
  # Go, Rust and Python factories and standalones and a TypeScript factory register, answer host calls, reload, and load cached, and a
  # Node standalone runs on the installation's node, with conflicting tools leading PATH; nothing is staged or written outside the cache.
  acceptance = acceptance "pig-acceptance" pig "";

  # A missing and a mismatched installed SDK are reported and nothing is staged in their place.
  broken-installation = acceptance "pig-broken-installation" broken "--broken";

  # An installation built without Python refuses its Python extensions instead of running a python3 or uv from PATH.
  without-python = acceptance "pig-without-python" (pig.override {
    withPythonExtensions = false;
  }) "--without-python";

  # The libraries the Node runtime's native helper links stay in the package closure, so garbage collection cannot remove them.
  closure = runCommand "pig-closure" { closure = closureInfo { rootPaths = [ pig ]; }; } (
    ''
      grep -qx ${pig.toolchain} "$closure/store-paths"
    ''
    + lib.optionalString stdenv.hostPlatform.isLinux ''
      grep -qx ${lib.getLib libxcb} "$closure/store-paths"
    ''
    + ''
      touch $out
    ''
  );
}
