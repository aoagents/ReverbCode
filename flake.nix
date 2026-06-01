{
  description = "agent-orchestrator development shell";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      nixpkgs,
      flake-utils,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        go = pkgs.go_1_25;
        agentOrchestratorDev = pkgs.writeShellApplication {
          name = "agent-orchestrator";
          runtimeInputs = [
            pkgs.coreutils
            pkgs.nodejs_22
          ];
          text = ''
            root="$PWD"
            while [ "$root" != "/" ] && { [ ! -f "$root/backend/go.mod" ] || [ ! -f "$root/frontend/package.json" ]; }; do
              root="$(dirname "$root")"
            done

            if [ ! -f "$root/backend/go.mod" ] || [ ! -f "$root/frontend/package.json" ]; then
              echo "Unable to find the agent-orchestrator workspace root."
              exit 1
            fi

            cd "$root/frontend"
            exec npm start "$@"
          '';
        };
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = [
            agentOrchestratorDev
            go
            pkgs.nodejs_22
            pkgs.pnpm_10
            pkgs.just
          ];

          shellHook = ''
            export GOROOT="${go}/share/go"
            export GOPATH="$PWD/.go"
            export GOBIN="$GOPATH/bin"
            export PNPM_HOME="$PWD/.pnpm"
            export PATH="$GOBIN:$PNPM_HOME:$PATH"
          '';
        };
      }
    );
}
