{
  config,
  lib,
  pkgs,
  ...
}:
{
  # Packages
  packages = with pkgs; [
    go
    gotools
    gopls
    govulncheck
    golangci-lint
    mockgen
    treefmt
    gofumpt
  ];

  # Cachix
  cachix.enable = false;

  # Go
  env = {
    GOTOOLCHAIN = lib.mkForce "auto";
    TEST_REDIS_ADDRESS = "${config.services.redis.bind}:${toString config.services.redis.port}";
  };
  languages.go = {
    enable = true;
    package = pkgs.go;
  };

  treefmt = {
    enable = true;
    config.programs = {
      nixfmt.enable = true;
      gofumpt.enable = true;
      yamlfmt.enable = true;
      typos.enable = true;
    };
  };

  tasks = {
    "go:mod" = {
      exec = ''
        go mod tidy
        go mod download
      '';
      description = "Tidy and download Go modules";
    };

    "go:gen" = {
      exec = ''
        go generate ./...
      '';
      description = "Run Go generate";
    };

    "go:lint" = {
      exec = ''
        modernize ./...
        golangci-lint run ./...
      '';
      description = "Run Go linters";
    };

    "go:lint-fix" = {
      exec = ''
        modernize --fix ./...
        golangci-lint run --fix ./...
        treefmt
      '';
      description = "Fix Go lint issues and format";
    };

    "go:test" = {
      exec = ''
        go test -race -count=1 -timeout=90s -cover ./...
      '';
      description = "Run Go tests";
    };

    "go:test-ci" = {
      exec = ''
        devenv up -d
        go test -race -timeout=90s -cover ./...
        devenv processes down
      '';
      description = "Run Go tests with services";
    };
  };

  # Git hooks
  git-hooks.hooks = {
    treefmt.enable = true;

    lint = {
      enable = true;
      name = "lint";
      description = "Go Lint";
      entry = "devenv tasks run go:lint";
      types = [ "go" ];
      pass_filenames = false;
    };
  };

  services.redis = {
    enable = true;
    bind = "127.0.0.1";
    port = 6379;
  };
}
