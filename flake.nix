{
  description = "Multi-agent orchestration system for Claude Code with persistent work tracking";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    beads.url = "github:gastownhall/beads";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      beads,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };
        beadsPkg = beads.packages.${system}.default;

        # The git revision this derivation was built from. A clean git tree has
        # `self.rev`; a dirty one only `self.dirtyRev` (rev + "-dirty"); a
        # non-git source has neither. Stamping it is what makes two nix builds
        # tellable apart: buildGoModule builds from a store copy with no .git,
        # so `debug.ReadBuildInfo()` reports no vcs.revision and every nix gt
        # otherwise prints an identical "1.2.1 (dev)" (gt-prx, gt-3pk).
        gtCommit = self.rev or (self.dirtyRev or "");
      in
      {
        packages = {
          gt = pkgs.buildGoModule {
            pname = "gt";
            version = "1.0.0";
            src = ./.;
            vendorHash = "sha256-ZUEQQ0br+5UQnk/XLM7NLDCd1qA93VOho1iQ3q3RUm8=";

            # The module path is github.com/steveyegge/gastown (go.mod); an -X
            # naming any other path is silently discarded by the linker, which
            # is why nix builds reported Build="dev" despite setting it here.
            ldflags = [
              "-X github.com/steveyegge/gastown/internal/cmd.Build=nix"
              "-X github.com/steveyegge/gastown/internal/cmd.BuiltProperly=1"
            ]
            ++ pkgs.lib.optional (gtCommit != "")
              "-X github.com/steveyegge/gastown/internal/cmd.Commit=${gtCommit}";

            subPackages = [ "cmd/gt" ];

            # go-icu-regex (pulled in via dolthub deps) is cgo and needs ICU headers/libs.
            buildInputs = [ pkgs.icu ];

            meta = with pkgs.lib; {
              description = "Multi-agent orchestration system for Claude Code with persistent work tracking";
              homepage = "https://github.com/gastownhall/gastown";
              license = licenses.mit;
              mainProgram = "gt";
            };
          };
          default = self.packages.${system}.gt;
        };

        apps = {
          gt = flake-utils.lib.mkApp {
            drv = self.packages.${system}.gt;
          };
          default = self.apps.${system}.gt;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = [
            beadsPkg
            pkgs.go_1_26
            pkgs.gopls
            pkgs.gotools
            pkgs.go-tools
          ];
        };
      }
    );
}
