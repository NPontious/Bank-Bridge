{
  description = "Bank Bridge - Parses bank alert emails into normalized finance transactions";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs supportedSystems (system: f system (import nixpkgs { inherit system; }));
    in
    {
      packages = forAllSystems (system: pkgs: rec {
        bank-bridge = pkgs.buildGoModule {
          pname = "bank-bridge";
          version = "0.1.0";
          src = ./.;

          # Set vendorHash to null or use actual hash
          vendorHash = "sha256-7Zh8l5JfaYtEbyVDHxbT/KbVrRqEvfPBCeMY/yKlWR8=";

          env = {
            CGO_ENABLED = 0;
          };

          ldflags = [
            "-s"
            "-w"
          ];

          meta = with pkgs.lib; {
            description = "Bank Bridge for email transaction parsing and self-hosted finance syncing";
            homepage = "https://github.com/NPontious/Bank-Bridge";
            license = licenses.mit;
          };
        };

        default = bank-bridge;
      });

      devShells = forAllSystems (system: pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            sqlite
          ];

          shellHook = ''
            echo "Bank Bridge dev environment active."
          '';
        };
      });

      nixosModules = rec {
        bank-bridge = import ./nix/nixos-module.nix;
        default = { config, ... }: {
          imports = [ bank-bridge ];
          services.bank-bridge.package = nixpkgs.lib.mkDefault self.packages.${config.nixpkgs.system}.default;
        };
      };
    };
}
