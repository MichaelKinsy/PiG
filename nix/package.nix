# SPDX-License-Identifier: MIT
{
  lib,
  stdenv,
  buildGo127Module,
  buildEnv,
  go_1_27,
  cargo,
  rustc,
  python3,
  nodejs,
  fd,
  ripgrep,
  wl-clipboard,
  xclip,
  libxcb,
  autoPatchelfHook,
  makeWrapper,
  rev ? "nix",
  # The extension languages this installation builds and runs. A language left out has no toolchain in the installation, and its
  # extensions report the missing toolchain instead of using one from PATH.
  withGoExtensions ? true,
  withRustExtensions ? true,
  withPythonExtensions ? true,
  withNodeExtensions ? true,
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

  # The toolchains PiG builds and launches extensions with. The binary reads them from this one directory and never from PATH
  # (internal/toolchain, D195): cargo runs rustc and the C linker by name, and cgo runs the C compiler, from the same directory.
  toolchain = buildEnv {
    name = "pig-extension-toolchain-${version}";
    paths =
      lib.optionals withGoExtensions [ go_1_27 ]
      ++ lib.optionals withRustExtensions [
        cargo
        rustc
      ]
      ++ lib.optionals withPythonExtensions [ python3 ]
      ++ lib.optionals withNodeExtensions [ nodejs ]
      ++ lib.optionals (withGoExtensions || withRustExtensions) [ stdenv.cc ];
    pathsToLink = [ "/bin" ];
  };

  # Pi's own helpers, as in Pi's flake: fd and ripgrep for the find and grep tools, and the clipboard commands for image paste.
  helpers = [
    fd
    ripgrep
  ]
  ++ lib.optionals stdenv.hostPlatform.isLinux [
    wl-clipboard
    xclip
  ];

  # The Node runtime's native helper for the host platform (pi-tui's X11 clipboard on Linux).
  nativeHelper =
    let
      platform = if stdenv.hostPlatform.isDarwin then "darwin" else "linux";
      arch = if stdenv.hostPlatform.isAarch64 then "arm64" else "x64";
      file = if stdenv.hostPlatform.isDarwin then "darwin-platform.node" else "linux-platform-x11.node";
    in
    "shims/pi-dist/pi-tui/native/${platform}/prebuilds/${platform}-${arch}/${file}";
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

  nativeBuildInputs = [
    makeWrapper
  ]
  ++ lib.optionals stdenv.hostPlatform.isLinux [ autoPatchelfHook ];

  # The native helpers of the Node runtime link against these; autoPatchelfHook points them at the store.
  buildInputs = lib.optionals stdenv.hostPlatform.isLinux [
    stdenv.cc.cc.lib
    libxcb
  ];

  ldflags = [
    "-s"
    "-w"
    "-X github.com/MichaelKinsy/PiG/coding/cli.Build=${rev}"
    "-X github.com/MichaelKinsy/PiG/internal/toolchain.installedToolchainDir=${toolchain}/bin"
  ];

  # The SDKs are installed in this package's own output, whose path is known only while it builds.
  preBuild = ''
    ldflags+=" -X github.com/MichaelKinsy/PiG/internal/pigsdklock.installedSDKRoot=$out/share/pig/sdk"
  '';

  doCheck = false;

  postInstall = ''
    # The binary writes the SDKs and the TypeScript extension runtime it embeds, so their content hashes match it by construction.
    HOME="$TMPDIR/home" PIG_HOME="$TMPDIR/pig-home" $out/bin/pig reload --install-sdks "$out/share/pig/sdk"

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

    # Every installed SDK and the runtime match the binary, and reload uses them in place.
    reload="$($out/bin/pig reload)"
    echo "$reload"
    for lang in go python rust; do
      grep -q "^pig reload: using installed $lang SDK at $out/share/pig/sdk/" <<<"$reload"
    done
    if [ -n "$(find "$PIG_HOME" -name .pig-sdk-version)" ]; then
      echo "pig reload staged an SDK copy under the config root" >&2
      exit 1
    fi

    # Load the host platform's native helper, so a dependency missing from the closure fails the build rather than disabling clipboard
    # support at run time.
    helper="$(echo $out/share/pig/sdk/node-runtime-*)/${nativeHelper}"
    ${lib.getExe nodejs} -e "require(process.argv[1])" "$helper"

    runHook postInstallCheck
  '';

  passthru = {
    inherit toolchain;
  };

  meta = {
    description = "Go implementation of the Pi coding agent";
    homepage = "https://github.com/MichaelKinsy/PiG";
    # PiG is MIT. The binary and the installed Node runtime also carry the third-party works listed in THIRD_PARTY_NOTICES.md; all
    # license texts, including PSF-2.0 and the ICU data terms that have no identifier here, are installed in share/licenses/pig.
    license = with lib.licenses; [
      mit
      asl20
      bsd3
      blueOak100
      cc-by-40
      isc
      unicode-30
    ];
    # The Node runtime ships prebuilt native helpers (pi-tui's platform modules), with their sources beside them.
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
