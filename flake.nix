{
  description = "FreeBSD virtual-machine images for LXD";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: rec {
        # UEFI firmware for the aarch64 builder VM (image/build.sh -a arm64).
        # nixpkgs only ships OVMF for the host architecture, so take Debian's
        # build of EDK2 for AArch64 from snapshot.debian.org (a stable URL).
        qemu-efi-aarch64 = pkgs.stdenvNoCC.mkDerivation {
          pname = "qemu-efi-aarch64";
          version = "2025.02-8+deb13u1";
          src = pkgs.fetchurl {
            url = "https://snapshot.debian.org/archive/debian/20260104T203950Z/pool/main/e/edk2/qemu-efi-aarch64_2025.02-8%2Bdeb13u1_all.deb";
            hash = "sha256-oAskEaeciur9lafIaKw80aq1kvGvn/+WXwLYGmJSdu0=";
          };
          nativeBuildInputs = [ pkgs.dpkg ];
          unpackPhase = "dpkg-deb -x $src .";
          installPhase = ''
            mkdir -p $out/share/qemu-efi-aarch64
            cp usr/share/qemu-efi-aarch64/QEMU_EFI.fd $out/share/qemu-efi-aarch64/
          '';
        };

        default = qemu-efi-aarch64;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            # agent
            go_1_27
            gopls
            gotools
            golangci-lint
            # image/build.sh
            qemu
            cdrkit
            xz
            curl
            gnutar
            python3
            self.packages.${pkgs.stdenv.hostPlatform.system}.qemu-efi-aarch64
          ];

          # image/build.sh picks the aarch64 UEFI firmware up from here.
          QEMU_EFI_AARCH64 = "${self.packages.${pkgs.stdenv.hostPlatform.system}.qemu-efi-aarch64}/share/qemu-efi-aarch64/QEMU_EFI.fd";
        };
      });
    };
}
