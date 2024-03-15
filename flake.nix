{
  description = "Wings";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = {...} @ inputs:
    inputs.flake-parts.lib.mkFlake {inherit inputs;} {
      systems = ["aarch64-darwin" "aarch64-linux" "x86_64-darwin" "x86_64-linux"];

      imports = [
        inputs.treefmt-nix.flakeModule
      ];

      perSystem = {system, ...}: let
        pkgs = import inputs.nixpkgs {inherit system;};
      in {
         devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go_1_22
            gofumpt
            golangci-lint
            gotools 
          ];
        };

        treefmt = {
          projectRootFile = "flake.nix";

          programs = {
            alejandra.enable = true;
            deadnix.enable = true;
            gofumpt = {
              enable = true;
              extra = true;
            };
            shellcheck.enable = true;
            shfmt = {
              enable = true;
              indent_size = 0; # 0 causes shfmt to use tabs
            };
            yamlfmt.enable = true;
          };
        };
      };
    };
}
