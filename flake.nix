{
  description = "Nix flake for open-chat: self-hosted chat server with embedded AI bots, tools and integrations";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      open-chat = system: nixpkgs.legacyPackages.${system}.callPackage ./nix/package.nix { };
    in
    {
      packages = forAllSystems (system: {
        open-chat = open-chat system;
        default = self.packages.${system}.open-chat;
      });

      apps = forAllSystems (
        system:
        let
          app = {
            type = "app";
            program = "${self.packages.${system}.open-chat}/bin/open-chat";
            meta.description = "Run the open-chat server";
          };
        in
        {
          default = app;
          open-chat = app;
        }
      );

      overlays.default = final: prev: {
        open-chat = open-chat final.stdenv.hostPlatform.system;
      };

      nixosModules = {
        open-chat = import ./nix/nixos-module.nix;
        default = self.nixosModules.open-chat;
      };

      checks = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          nixosEval = nixpkgs.lib.nixosSystem {
            inherit system;
            modules = [
              self.nixosModules.open-chat
              {
                services.open-chat.enable = true;
                system.stateVersion = "24.11";
              }
            ];
          };
        in
        {
          open-chat = self.packages.${system}.open-chat;
          nixos-module-eval = pkgs.writeText "open-chat-execstart" (
            nixosEval.config.systemd.services.open-chat.serviceConfig.ExecStart
          );
        }
      );
    };
}