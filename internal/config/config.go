package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port             int
	DBPath           string
	APIToken         string
	AccountRulesJSON string
	IMAPEnabled      bool
	IMAPHost         string
	IMAPPort         int
	IMAPUser         string
	IMAPPassword     string
	IMAPMailbox      string
	IMAPFrom         string
	PollInterval     time.Duration
}

func Load() *Config {
	cfg := &Config{
		Port:             getEnvInt("PORT", 8080),
		DBPath:           getEnv("DB_PATH", "./data/bank-bridge.db"),
		APIToken:         readEnvOrFile("API_TOKEN", "API_TOKEN_FILE", ""),
		AccountRulesJSON: readEnvOrFile("ACCOUNT_RULES", "ACCOUNT_RULES_FILE", ""),
		IMAPHost:         getEnv("IMAP_HOST", "imap.gmail.com"),
		IMAPPort:         getEnvInt("IMAP_PORT", 993),
		IMAPUser:         getEnv("IMAP_USER", ""),
		IMAPPassword:     readEnvOrFile("IMAP_PASSWORD", "IMAP_PASSWORD_FILE", ""),
		IMAPMailbox:      getEnv("IMAP_MAILBOX", "INBOX"),
		IMAPFrom:         getEnv("IMAP_SEARCH_FROM", ""),
		PollInterval:     getEnvDuration("POLL_INTERVAL", 2*time.Minute),
	}

	imapExplicit := getEnv("IMAP_ENABLED", "")
	if imapExplicit != "" {
		cfg.IMAPEnabled = (imapExplicit == "true" || imapExplicit == "1")
	} else {
		cfg.IMAPEnabled = (cfg.IMAPUser != "" && cfg.IMAPPassword != "")
	}

	return cfg
}

func getEnv(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}

func getEnvInt(key string, def int) int {
	if val := os.Getenv(key); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return def
}

func readEnvOrFile(envKey, fileKey, def string) string {
	if filePath := os.Getenv(fileKey); filePath != "" {
		if content, err := os.ReadFile(filePath); err == nil {
			return strings.TrimSpace(string(content))
		}
	}
	if val := os.Getenv(envKey); val != "" {
		return strings.TrimSpace(val)
	}
	return def
}
