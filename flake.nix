{
  description = "entish: a non-POSIX shell with a strict, functional language";

  nixConfig = {
    extra-substituters = ["https://kata.cachix.org"];
    extra-trusted-public-keys = ["kata.cachix.org-1:l3Qa7glGu6EVSEYAoH7tqWXacHx7ghhYUbBGPucxmm8="];
  };

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = {nixpkgs, ...}: let
    inherit (nixpkgs) lib;
    forAllSystems = f:
      lib.genAttrs ["x86_64-linux" "aarch64-linux" "aarch64-darwin"]
      (system: f nixpkgs.legacyPackages.${system});
  in {
    packages = forAllSystems (pkgs: {
      default = pkgs.buildGoModule {
        pname = "entish";
        version = "0.0.0";

        src = lib.fileset.toSource {
          root = ./.;
          fileset = lib.fileset.unions [
            ./go.mod
            ./go.sum
            ./config.ent
            (lib.fileset.fileFilter (f: f.hasExt "go") ./.)
          ];
        };

        vendorHash = "sha256-sJn4yiUb227dGjpFF6dw/oXOLQ/2mdSikqymhkDchSM=";

        doCheck = false;

        ldflags = ["-s" "-w"];

        meta.mainProgram = "entish";
      };
    });

    devShells = forAllSystems (pkgs: {
      default = pkgs.mkShell {packages = [pkgs.go];};
    });
  };
}
