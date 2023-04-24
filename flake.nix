{
  description = "An over-engineered pint World in C";

  # Nixpkgs / NixOS version to use.
  inputs.nixpkgs.url = "nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let

      # to work with older version of flakes
      lastModifiedDate = self.lastModifiedDate or self.lastModified or "19700101";

      # Generate a user-friendly version number.
      version = builtins.substring 0 8 lastModifiedDate;

      # System types to support.
      supportedSystems = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];

      # Helper function to generate an attrset '{ x86_64-linux = f "x86_64-linux"; ... }'.
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;

      # Nixpkgs instantiated for supported system types.
      nixpkgsFor = forAllSystems (system: import nixpkgs { inherit system; overlays = [ self.overlay ]; });

    in

    {

      # A Nixpkgs overlay.
      overlay = final: prev: {

        pint = with final; stdenv.mkDerivation rec {
          pname = "pint";
          inherit version;

          src = ./.;

          buildPhase = ''
            # failed to initialize build cache at /homeless-shelter/.cache/go-build: mkdir /homeless-shelter: permission denied
            export HOME=$(mktemp -d)

            make
          '';
          installPhase = "mkdir -p $out/bin; install -t $out/bin pint";

          # nativeBuildInputs = [ go gopls gotools go-tools cmake ];
          nativeBuildInputs = [ go gopls gotools go-tools ];
        };

      };

      devShells = forAllSystems (system:
        let pkgs = nixpkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            buildInputs = with pkgs; [ go gopls gotools go-tools ];
          };
        });

      # Provide some binary packages for selected system types.
      packages = forAllSystems (system:
        {
          inherit (nixpkgsFor.${system}) pint;
        });

      # The default package for 'nix build'. This makes sense if the
      # flake provides only one package or there is a clear "main"
      # package.
      defaultPackage = forAllSystems (system: self.packages.${system}.pint);

      # A NixOS module, if applicable (e.g. if the package provides a system service).
      nixosModules.pint =
        { pkgs, ... }:
        {
          nixpkgs.overlays = [ self.overlay ];

          environment.systemPackages = [ pkgs.pint ];

          #systemd.services = { ... };
        };

      # Tests run by 'nix flake check' and by Hydra.
      checks = forAllSystems
        (system:
          with nixpkgsFor.${system};

          {
            inherit (self.packages.${system}) pint;

            # Additional tests, if applicable.
            test = stdenv.mkDerivation {
              pname = "pint";
              inherit version;

              buildInputs = [ pint ];

              dontUnpack = true;

              buildPhase = ''
                echo 'running some integration tests'
                [[ $(pint) = 'pint Nixers!' ]]
              '';

              installPhase = "mkdir -p $out";
            };
          }

          // lib.optionalAttrs stdenv.isLinux {
            # A VM test of the NixOS module.
            vmTest =
              with import (nixpkgs + "/nixos/lib/testing-python.nix")
                {
                  inherit system;
                };

              makeTest {
                nodes = {
                  client = { ... }: {
                    imports = [ self.nixosModules.pint ];
                  };
                };

                testScript =
                  ''
                    start_all()
                    client.wait_for_unit("multi-user.target")
                    client.succeed("pint")
                  '';
              };
          }
        );

    };
}

