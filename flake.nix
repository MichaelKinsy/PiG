# SPDX-License-Identifier: MIT
{
  description = "PiG, a Go implementation of the Pi coding agent";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    # nixpkgs unstable no longer supports Intel macOS. As in Pi's flake, the final Darwin branch that does builds the x86_64-darwin
    # package.
    nixpkgs-darwin-x64.url = "github:NixOS/nixpkgs/nixpkgs-26.05-darwin";
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-darwin-x64,
    }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      pkgsFor =
        system:
        (if system == "x86_64-darwin" then nixpkgs-darwin-x64 else nixpkgs).legacyPackages.${system};
      rev = self.shortRev or self.dirtyShortRev or "nix";
      packageFor = pkgs: pkgs.callPackage ./nix/package.nix { inherit rev; };
    in
    {
      packages = forAllSystems (
        system:
        let
          pig = packageFor (pkgsFor system);
        in
        {
          default = pig;
          inherit pig;
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = nixpkgs.lib.getExe self.packages.${system}.default;
          meta.description = "PiG coding agent";
        };
        pig = self.apps.${system}.default;
      });

      overlays.default = final: _previous: {
        pig = packageFor final;
      };

      checks = forAllSystems (system: {
        inherit (self.packages.${system}) pig;
      });

      formatter = forAllSystems (system: (pkgsFor system).nixfmt);
    };
}
