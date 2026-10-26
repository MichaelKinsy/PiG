# SPDX-License-Identifier: MIT
{
  lib,
  stdenv,
  buildGo127Module,
  nodejs,
  fd,
  ripgrep,
  wl-clipboard,
  xclip,
  makeWrapper,
  rev ? "nix",
}:

let
  root = ../.;

  # The pig release line and the full version `pig --version` prints, read from the file `make set-version` and the upstream pin write.
  pigversion = builtins.readFile (root + "/internal/coding/pigversion/pigversion.go");
  constant =
    name: pattern:
    let
      match = builtins.match ".*\nconst ${name} = ${pattern}\n.*" pigversion;
    in
    if match == null then
      throw "nix/package.nix: ${name} not found in pigversion.go"
    else
      builtins.head match;
  version = constant "PigVersion" ''"([^"]+)"'';
  fullVersion = version + constant "Version" ''PigVersion \+ "([^"]+)"'';

  # The build reads Go sources, embedded files, and the workspace modules. Tests, parity fixtures, and
  # documentation are left out, so editing them does not rebuild the package.
  src = lib.fileset.toSource {
    inherit root;
    fileset = lib.fileset.difference root (
      lib.fileset.unions (
        map lib.fileset.maybeMissing [
          (root + "/.github")
          (root + "/bench")
          (root + "/docs")
          (root + "/handoffs")
          (root + "/plans")
          (root + "/research")
          (root + "/test")
          (root + "/flake.nix")
          (root + "/flake.lock")
          (root + "/nix")
        ]
      )
    );
  };

  # Platforms whose module sets the fixed-output module cache covers, so its hash is the same on every system.
  goPlatforms = [
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
  ];

  # Pi's own helpers, as in Pi's flake: fd and ripgrep for the find and grep tools, and the clipboard commands for image paste.
  helpers = [
    fd
    ripgrep
  ]
  ++ lib.optionals stdenv.hostPlatform.isLinux [
    wl-clipboard
    xclip
  ];
in
buildGo127Module {
  pname = "pig";
  inherit version src;

  subPackages = [ "cmd/pig" ];

  # The repository is a Go workspace: cmd/pig links extensions/sdk from this revision, not the published module.
  # `go mod vendor` refuses workspaces, and `go work vendor` also vendors every `go tool` dependency. The module
  # cache below holds exactly the modules cmd/pig links on each supported platform.
  proxyVendor = true;
  vendorHash = "sha256-SnUVANkiNlaTXyrschLLH/MTcyqAmNRVvbSAA8ml/Qk=";
  modBuildPhase = ''
    runHook preBuild
    export GIT_SSL_CAINFO=$NIX_SSL_CERT_FILE
    for platform in ${lib.concatStringsSep " " goPlatforms}; do
      GOOS=''${platform%/*} GOARCH=''${platform#*/} CGO_ENABLED=0 go list -deps ./cmd/pig > /dev/null
    done
    runHook postBuild
  '';

  env.CGO_ENABLED = "0";

  nativeBuildInputs = [ makeWrapper ];

  ldflags = [
    "-s"
    "-w"
    "-X github.com/MichaelKinsy/PiG/coding/cli.Build=${rev}"
  ];

  doCheck = false;

  postInstall = ''
    install -Dm644 -t "$out/share/licenses/pig" LICENSE NOTICE THIRD_PARTY_NOTICES.md
    cp -r LICENSES "$out/share/licenses/pig/LICENSES"

    # As in Pi's flake: PiG's helpers come first, and Node goes last so that `npm` is available for package installs while the
    # shells the bash tool starts keep the user's own node and npm.
    wrapProgram "$out/bin/pig" \
      --prefix PATH : ${lib.makeBinPath helpers} \
      --suffix PATH : ${lib.makeBinPath [ nodejs ]}
  '';

  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    export HOME="$TMPDIR/check-home" PIG_HOME="$TMPDIR/check-pig-home"
    test "$($out/bin/pig --version)" = "${fullVersion}"
    runHook postInstallCheck
  '';

  meta = {
    description = "Go implementation of the Pi coding agent";
    homepage = "https://github.com/MichaelKinsy/PiG";
    # PiG is MIT. The binary also carries the third-party works listed in THIRD_PARTY_NOTICES.md; all license texts, including
    # PSF-2.0 and the ICU data terms that have no identifier here, are installed in share/licenses/pig.
    license = with lib.licenses; [
      mit
      asl20
      bsd3
      blueOak100
      cc-by-40
      isc
      unicode-30
    ];
    # The binary embeds the TypeScript extension runtime's prebuilt native helpers (pi-tui's platform modules).
    sourceProvenance = with lib.sourceTypes; [
      fromSource
      binaryNativeCode
    ];
    mainProgram = "pig";
    platforms = [
      "aarch64-darwin"
      "aarch64-linux"
      "x86_64-darwin"
      "x86_64-linux"
    ];
  };
}
