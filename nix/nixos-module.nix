{
  config,
  lib,
  pkgs,
  ...
}:

let
  inherit (lib)
    mkEnableOption
    mkIf
    mkOption
    optionals
    optionalString
    types
    ;

  cfg = config.services.open-chat;

  dataDir = "/var/lib/open-chat";
  defaultDbPath = "${dataDir}/data.db";

  redisModes = [
    "auto"
    "embedded"
    "external"
  ];
in
{
  options.services.open-chat = {
    enable = mkEnableOption "the Open Chat server";

    package = mkOption {
      type = types.package;
      default = pkgs.callPackage ./package.nix { };
      defaultText = lib.literalExpression "open-chat";
      description = "The open-chat package to use.";
    };

    host = mkOption {
      type = types.str;
      default = "127.0.0.1";
      description = "Address the server binds to.";
    };

    port = mkOption {
      type = types.port;
      default = 1984;
      description = "Port the server listens on.";
    };

    dbPath = mkOption {
      type = types.str;
      default = defaultDbPath;
      description = ''
        Path to the SQLite database. The parent directory is created by
        systemd's `StateDirectory`; only paths below `${dataDir}` are writable
        by the service (see `ProtectSystem`).
      '';
    };

    redisMode = mkOption {
      type = types.enum redisModes;
      default = "auto";
      description = ''
        Redis mode. `auto` uses an external Redis when reachable and otherwise
        falls back to the in-process embedded Redis; `embedded` always uses the
        in-process instance; `external` requires the configured external Redis.
      '';
    };

    redisUrl = mkOption {
      type = types.str;
      default = "redis://127.0.0.1:6379/0";
      description = "Redis URL used when `redisMode` is not `embedded`.";
    };

    redisAddr = mkOption {
      type = types.str;
      default = "127.0.0.1:6379";
      description = "Redis address used when `redisUrl` is empty.";
    };

    redisPassword = mkOption {
      type = types.str;
      default = "";
      description = "Redis password, if any.";
    };

    redisDb = mkOption {
      type = types.int;
      default = 0;
      description = "Redis database index.";
    };

    rootCredentialsFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      example = "/run/secrets/open-chat-root";
      description = ''
        Path to an environment file containing `ROOT_CREDENTIALS=admin:password`.
        The admin account is bootstrapped from it on first start. Keep the file
        out of the Nix store (e.g. use sops-nix or agenix) and readable by the
        `open-chat` user.
      '';
    };

    configFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      example = "/etc/open-chat/open-chat.json";
      description = ''
        Optional path to an open-chat JSON/YAML config document, passed via
        `--config`. Values already present in the environment take precedence
        unless the document enables `config-override-env`.
      '';
    };

    environment = mkOption {
      type = types.attrsOf types.str;
      default = { };
      example = {
        DEBUG = "false";
        OPENROUTER_API_KEY = "sk-...";
      };
      description = ''
        Extra environment variables for the server, e.g. provider API keys.
        Prefer `environmentFiles`/`rootCredentialsFile` for secrets: values set
        here end up world-readable in the Nix store and the systemd unit.
      '';
    };

    environmentFiles = mkOption {
      type = types.listOf types.path;
      default = [ ];
      description = "Additional `EnvironmentFile`s for the service.";
    };

    extraArgs = mkOption {
      type = types.listOf types.str;
      default = [ ];
      example = [ "--cors-allowed-origins" "https://chat.example.com" ];
      description = "Extra arguments appended to the `open-chat server` command.";
    };

    openFirewall = mkOption {
      type = types.bool;
      default = false;
      description = "Open `port` in the firewall. Enable when the server is not bound to localhost.";
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.redisMode != "external" || cfg.redisUrl != "";
        message = "services.open-chat.redisMode = \"external\" requires services.open-chat.redisUrl to be set.";
      }
    ];

    users.groups.open-chat = { };

    users.users.open-chat = {
      isSystemUser = true;
      group = "open-chat";
      home = dataDir;
      description = "Open Chat service user";
    };

    networking.firewall.allowedTCPPorts = optionals cfg.openFirewall [ cfg.port ];

    systemd.services.open-chat = {
      description = "Open Chat server";
      documentation = [ "https://github.com/msgmate-io/open-chat-go" ];
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];

      environment = {
        HOST = cfg.host;
        PORT = toString cfg.port;
        DB_BACKEND = "sqlite";
        DB_PATH = cfg.dbPath;
        REDIS_MODE = cfg.redisMode;
        REDIS_URL = cfg.redisUrl;
        REDIS_ADDR = cfg.redisAddr;
        REDIS_PASSWORD = cfg.redisPassword;
        REDIS_DB = toString cfg.redisDb;
      }
      // cfg.environment;

      serviceConfig = {
        Type = "simple";
        ExecStart =
          "${lib.getExe cfg.package} server"
          + optionalString (cfg.configFile != null) " --config ${cfg.configFile}"
          + " --host ${cfg.host}"
          + " --port ${toString cfg.port}"
          + " --db-path ${cfg.dbPath}"
          + lib.concatMapStrings (arg: " ${arg}") cfg.extraArgs;

        User = "open-chat";
        Group = "open-chat";
        WorkingDirectory = dataDir;
        StateDirectory = "open-chat";

        EnvironmentFile = optionals (cfg.rootCredentialsFile != null) [ cfg.rootCredentialsFile ]
          ++ cfg.environmentFiles;

        Restart = "on-failure";
        RestartSec = 5;
        TimeoutStopSec = 30;

        # Hardening. The server only needs its state directory and outbound
        # network access (LLM providers, external Redis).
        NoNewPrivileges = true;
        PrivateDevices = true;
        PrivateTmp = true;
        ProtectClock = true;
        ProtectControlGroups = true;
        ProtectHome = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectProc = "invisible";
        ProtectSystem = "strict";
        RestrictAddressFamilies = [
          "AF_UNIX"
          "AF_INET"
          "AF_INET6"
          "AF_NETLINK"
        ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RemoveIPC = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [
          "@system-service"
          "~@privileged"
        ];
        CapabilityBoundingSet = [ "" ];
        AmbientCapabilities = [ "" ];
      };
    };
  };
}