{
  # pare — `nix run github:akira-toriyama/pare` or `nix profile install`.
  #
  # The primary distribution is the Homebrew cask (see .goreleaser.yaml); this
  # flake is the secondary, source-built channel. version stays "dev" on purpose
  # — a source build has no release number, so there is nothing to go stale (the
  # commit is stamped from the flake's own git rev instead).
  #
  # vendorHash pins the vendored go modules; when go.mod/go.sum change, set it
  # back to pkgs.lib.fakeHash, run `nix build`, and paste the hash nix prints
  # ("got: sha256-...").
  description = "Context-budget-aware output truncation for AI coding agents — head + error lines + tail within a byte budget";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        version = "dev";
        rev = self.rev or self.dirtyRev or "unknown";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "pare";
          inherit version;
          src = ./.;
          vendorHash = "sha256-7K17JaXFsjf163g5PXCb5ng2gYdotnZ2IDKk8KFjNj0=";
          ldflags = [
            "-s" "-w"
            "-X github.com/akira-toriyama/pare/internal/version.Version=${version}"
            "-X github.com/akira-toriyama/pare/internal/version.Commit=${rev}"
          ];
          subPackages = [ "cmd/pare" ];
          meta = with pkgs.lib; {
            description = "Context-budget-aware output truncation for AI coding agents";
            homepage = "https://github.com/akira-toriyama/pare";
            license = licenses.mit;
            mainProgram = "pare";
          };
        };

        apps.default = flake-utils.lib.mkApp {
          drv = self.packages.${system}.default;
          name = "pare";
        };

        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.golangci-lint pkgs.goreleaser pkgs.git-cliff ];
        };
      });
}
