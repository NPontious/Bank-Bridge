{ config, lib, pkgs, ... }:

with lib;

let
  cfg = config.services.bank-bridge;
in {
  options.services.bank-bridge = {
    enable = mkEnableOption "Bank Bridge service for bank email parsing and financial sync";

    package = mkOption {
      type = types.package;
      description = "The bank-bridge package to run.";
    };

    port = mkOption {
      type = types.port;
      default = 8080;
      description = "Port the HTTP API server listens on.";
    };

    dataDir = mkOption {
      type = types.path;
      default = "/var/lib/bank-bridge";
      description = "Directory to store SQLite database.";
    };

    environmentFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      description = "Environment file (e.g. from agenix/sops) containing secrets (IMAP_USER, IMAP_PASSWORD, API_TOKEN, ACCOUNT_RULES).";
    };

    accountRulesFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      description = "Path to JSON file containing custom account mapping and balance adjustment rules (for agenix/sops).";
    };

    apiTokenFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      description = "Path to file containing API bearer token for authentication.";
    };

    imap = {
      enable = mkOption {
        type = types.bool;
        default = false;
        description = "Whether to enable IMAP email polling.";
      };

      host = mkOption {
        type = types.str;
        default = "imap.gmail.com";
        description = "IMAP server hostname.";
      };

      port = mkOption {
        type = types.port;
        default = 993;
        description = "IMAP server port.";
      };

      user = mkOption {
        type = types.str;
        default = "";
        description = "IMAP username (e.g. Gmail address).";
      };

      passwordFile = mkOption {
        type = types.nullOr types.path;
        default = null;
        description = "Path to file containing IMAP password (e.g. Google App Password).";
      };

      mailbox = mkOption {
        type = types.str;
        default = "INBOX";
        description = "IMAP mailbox/folder to poll.";
      };

      searchFrom = mkOption {
        type = types.str;
        default = "";
        description = "Optional sender filter for IMAP search. Can also be set via IMAP_SEARCH_FROM in environmentFile.";
      };

      pollInterval = mkOption {
        type = types.str;
        default = "2m";
        description = "Polling interval for IMAP checks.";
      };
    };
  };

  config = mkIf cfg.enable {
    users.users.bank-bridge = {
      isSystemUser = true;
      group = "bank-bridge";
      description = "Bank Bridge daemon user";
      home = cfg.dataDir;
    };
    users.groups.bank-bridge = {};

    systemd.services.bank-bridge = {
      description = "Bank Bridge Service";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];

      serviceConfig = {
        ExecStart = "${cfg.package}/bin/bank-bridge";
        Restart = "always";
        RestartSec = "10s";

        # Sandboxing & isolation
        User = "bank-bridge";
        Group = "bank-bridge";
        StateDirectory = "bank-bridge";
        WorkingDirectory = cfg.dataDir;

        # Hardening
        ProtectSystem = "strict";
        ProtectHome = true;
        NoNewPrivileges = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectControlGroups = true;
        RestrictNamespaces = true;
      } // optionalAttrs (cfg.environmentFile != null) {
        EnvironmentFile = toString cfg.environmentFile;
      };

      environment = {
        PORT = toString cfg.port;
        DB_PATH = "${cfg.dataDir}/bank-bridge.db";
        IMAP_HOST = cfg.imap.host;
        IMAP_PORT = toString cfg.imap.port;
        IMAP_MAILBOX = cfg.imap.mailbox;
        POLL_INTERVAL = cfg.imap.pollInterval;
      } // optionalAttrs (cfg.imap.enable) {
        IMAP_ENABLED = "true";
      } // optionalAttrs (!cfg.imap.enable && cfg.environmentFile == null) {
        IMAP_ENABLED = "false";
      } // optionalAttrs (cfg.imap.searchFrom != "") {
        IMAP_SEARCH_FROM = cfg.imap.searchFrom;
      } // optionalAttrs (cfg.imap.user != "") {
        IMAP_USER = cfg.imap.user;
      } // optionalAttrs (cfg.accountRulesFile != null) {
        ACCOUNT_RULES_FILE = toString cfg.accountRulesFile;
      } // optionalAttrs (cfg.apiTokenFile != null) {
        API_TOKEN_FILE = toString cfg.apiTokenFile;
      } // optionalAttrs (cfg.imap.passwordFile != null) {
        IMAP_PASSWORD_FILE = toString cfg.imap.passwordFile;
      };
    };
  };
}
