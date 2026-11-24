package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NPontious/Bank-Bridge/internal/api"
	"github.com/NPontious/Bank-Bridge/internal/config"
	"github.com/NPontious/Bank-Bridge/internal/ingest"
	"github.com/NPontious/Bank-Bridge/internal/parser"
	_ "github.com/NPontious/Bank-Bridge/internal/parser/gridtable" // Register GridTable parser
	_ "github.com/NPontious/Bank-Bridge/internal/parser/summary"   // Register Summary & Balance Alert parser
	"github.com/NPontious/Bank-Bridge/internal/store"
)

func main() {
	parseFileFlag := flag.String("parse-file", "", "Path to a raw .eml file to parse and print JSON output (CLI mode)")
	portFlag := flag.Int("port", 0, "HTTP port (overrides PORT environment variable)")
	dbPathFlag := flag.String("db", "", "Path to SQLite database (overrides DB_PATH)")
	setBalanceFlag := flag.String("set-balance", "", "Account ID to update balance for (CLI mode)")
	balanceFlag := flag.Float64("balance", 0, "Balance amount for -set-balance")
	availableFlag := flag.Float64("available", 0, "Available balance for -set-balance (defaults to -balance if omitted)")
	reprocessFlag := flag.Bool("reprocess-skipped", false, "Reprocess all skipped emails in database (CLI mode)")
	flag.Parse()

	cfg := config.Load()
	if cfg.AccountRulesJSON != "" {
		if err := parser.LoadAccountRules(cfg.AccountRulesJSON); err != nil {
			log.Printf("[Config] Warning: failed to parse ACCOUNT_RULES: %v", err)
		} else {
			log.Printf("[Config] Loaded custom account rules")
		}
	}

	// CLI Mode: Parse single file and exit
	if *parseFileFlag != "" {
		runParseFileCLI(*parseFileFlag)
		return
	}

	if *portFlag > 0 {
		cfg.Port = *portFlag
	}
	if *dbPathFlag != "" {
		cfg.DBPath = *dbPathFlag
	}

	// CLI Mode: Reprocess skipped emails
	if *reprocessFlag {
		runReprocessSkipped(cfg.DBPath)
		return
	}

	// CLI Mode: Set account balance directly in SQLite
	if *setBalanceFlag != "" {
		db, err := store.New(cfg.DBPath)
		if err != nil {
			log.Fatalf("Failed to open database at %s: %v", cfg.DBPath, err)
		}
		defer db.Close()

		avail := *availableFlag
		if avail == 0 && *balanceFlag != 0 {
			avail = *balanceFlag
		}
		if err := db.UpdateAccountBalance(context.Background(), *setBalanceFlag, balanceFlag, &avail); err != nil {
			log.Fatalf("Failed to update balance: %v", err)
		}
		log.Printf("Successfully updated %s balance=%.2f available=%.2f in %s", *setBalanceFlag, *balanceFlag, avail, cfg.DBPath)
		return
	}

	log.Printf("Starting Bank Bridge on port %d...", cfg.Port)

	// Initialize Database
	db, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to initialize database at %s: %v", cfg.DBPath, err)
	}
	defer db.Close()
	log.Printf("Connected to database at %s", cfg.DBPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Automatically attempt to reprocess any previously skipped emails on startup
	go func() {
		skipped, err := db.GetSkippedEmails(ctx)
		if err == nil && len(skipped) > 0 {
			reprocessedCount := 0
			for _, email := range skipped {
				results, err := parser.ParseEmail(&email)
				if err == nil {
					email.Status = "parsed"
					email.Error = ""
					if err := db.SaveParsedResults(ctx, &email, results); err == nil {
						reprocessedCount++
						log.Printf("[Startup] Reprocessed previously skipped email %s (Subject: %q)", email.MessageID, email.Subject)
					}
				}
			}
			if reprocessedCount > 0 {
				log.Printf("[Startup] Successfully reprocessed %d previously skipped email(s)", reprocessedCount)
			}
		}
	}()

	// Optional IMAP Service
	var imapSvc *ingest.IMAPService
	if cfg.IMAPEnabled {
		imapCfg := ingest.IMAPConfig{
			Host:         cfg.IMAPHost,
			Port:         cfg.IMAPPort,
			Username:     cfg.IMAPUser,
			Password:     cfg.IMAPPassword,
			Mailbox:      cfg.IMAPMailbox,
			SearchFrom:   cfg.IMAPFrom,
			PollInterval: cfg.PollInterval,
		}
		imapSvc = ingest.NewIMAPService(imapCfg, db)
		imapSvc.Start(ctx)
	} else {
		log.Printf("IMAP is disabled or credentials not provided. Running in API / Webhook mode only.")
	}

	// HTTP API Server
	srv := api.NewServer(db, imapSvc, cfg.APIToken, cfg.Port)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	select {
	case <-sigCh:
		log.Println("Shutting down gracefully...")
	case err := <-serverErrCh:
		log.Printf("Fatal HTTP server error: %v", err)
	}

	if imapSvc != nil {
		imapSvc.Stop()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	log.Println("Bank Bridge stopped.")
}

func runParseFileCLI(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Error reading file %s: %v", path, err)
	}

	rawEmail, err := ingest.ParseRawEmail(data)
	if err != nil {
		log.Fatalf("Error parsing MIME headers: %v", err)
	}

	// Use temporary in-memory store
	tmpStore, err := store.New(":memory:")
	if err != nil {
		log.Fatalf("Error initializing temporary store: %v", err)
	}
	defer tmpStore.Close()

	results, err := ingest.IngestRawBytes(context.Background(), tmpStore, data)
	if err != nil {
		log.Fatalf("Error ingesting email: %v", err)
	}

	accounts, _ := tmpStore.GetAccounts(context.Background())
	txns, _ := tmpStore.GetTransactions(context.Background(), "", time.Time{}, 0, 0)

	output := map[string]any{
		"email": map[string]any{
			"message_id": rawEmail.MessageID,
			"from":       rawEmail.From,
			"subject":    rawEmail.Subject,
			"date":       rawEmail.Date,
		},
		"accounts":     accounts,
		"transactions": txns,
		"imported":     len(results),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(output)
}

func runReprocessSkipped(dbPath string) {
	db, err := store.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to open database at %s: %v", dbPath, err)
	}
	defer db.Close()

	ctx := context.Background()
	skipped, err := db.GetSkippedEmails(ctx)
	if err != nil {
		log.Fatalf("Failed to query skipped emails: %v", err)
	}

	log.Printf("Found %d skipped email(s) in %s", len(skipped), dbPath)
	successCount := 0
	for _, email := range skipped {
		results, err := parser.ParseEmail(&email)
		if err != nil {
			log.Printf("Still skipped (%s): %v", email.MessageID, err)
			continue
		}
		email.Status = "parsed"
		email.Error = ""
		if err := db.SaveParsedResults(ctx, &email, results); err != nil {
			log.Printf("Failed to save results for %s: %v", email.MessageID, err)
			continue
		}
		log.Printf("Successfully parsed email %s (Subject: %q) -> %d result(s)", email.MessageID, email.Subject, len(results))
		successCount++
	}
	log.Printf("Reprocessing complete: %d/%d successfully processed", successCount, len(skipped))
}

