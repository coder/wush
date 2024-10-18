{
  description = "Dev shell for Go backend and React frontend (using pnpm)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }: flake-utils.lib.eachDefaultSystem (system:
    let
      pkgs = import nixpkgs { inherit system; };
    in
    {
      devShell = pkgs.mkShell
        {
          buildInputs = with pkgs; [
            go
            nodejs
            pnpm
            binaryen # wasm-opt
          ];

          shellHook = ''
            exec $SHELL
          '';
        };
    });
}
