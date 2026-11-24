package ingest

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/NPontious/Bank-Bridge/internal/models"
	"github.com/NPontious/Bank-Bridge/internal/parser"
	"github.com/NPontious/Bank-Bridge/internal/store"
)

type IMAPConfig struct {
	Host         string
	Port         int
	Username     string
	Password     string
	Mailbox      string        // e.g. "INBOX"
	SearchFrom   string        // Filter header "From", e.g. "alerts@mybank.com"
	PollInterval time.Duration // e.g. 2 * time.Minute
}

type IMAPService struct {
	cfg     IMAPConfig
	store   *store.Store
	mu      sync.Mutex
	stopCh  chan struct{}
	running bool
}

func NewIMAPService(cfg IMAPConfig, s *store.Store) *IMAPService {
	if cfg.Mailbox == "" {
		cfg.Mailbox = "INBOX"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Minute
	}
	return &IMAPService{
		cfg:    cfg,
		store:  s,
		stopCh: make(chan struct{}),
	}
}

func (s *IMAPService) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	log.Printf("[IMAP] Starting IMAP poller for %s@%s:%d (interval: %v)", s.cfg.Username, s.cfg.Host, s.cfg.Port, s.cfg.PollInterval)

	// Run initial sync
	if err := s.Sync(ctx); err != nil {
		log.Printf("[IMAP] Initial sync error: %v", err)
	}

	ticker := time.NewTicker(s.cfg.PollInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				if err := s.Sync(ctx); err != nil {
					log.Printf("[IMAP] Sync error: %v", err)
				}
			}
		}
	}()
}

func (s *IMAPService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	close(s.stopCh)
	s.running = false
	log.Printf("[IMAP] Stopped IMAP poller")
}

func (s *IMAPService) Sync(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	client, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		return fmt.Errorf("failed to dial IMAP host: %w", err)
	}
	defer client.Close()

	if err := client.Login(s.cfg.Username, s.cfg.Password).Wait(); err != nil {
		return fmt.Errorf("IMAP login failed: %w", err)
	}
	defer client.Logout()

	if _, err := client.Select(s.cfg.Mailbox, nil).Wait(); err != nil {
		return fmt.Errorf("failed to select mailbox %q: %w", s.cfg.Mailbox, err)
	}

	// Build search criteria: past 30 days
	sinceDate := time.Now().AddDate(0, 0, -30)
	criteria := &imap.SearchCriteria{
		Since: sinceDate,
	}
	if s.cfg.SearchFrom != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{
			Key:   "From",
			Value: s.cfg.SearchFrom,
		})
	}

	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return fmt.Errorf("UID search failed: %w", err)
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return nil
	}

	bodySection := &imap.FetchItemBodySection{}
	fetchOpts := &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}

	var numSet imap.UIDSet
	for _, uid := range uids {
		numSet.AddNum(uid)
	}

	fetchCmd := client.Fetch(numSet, fetchOpts)
	defer fetchCmd.Close()

	msgs, err := fetchCmd.Collect()
	if err != nil {
		return fmt.Errorf("failed collecting fetch messages: %w", err)
	}

	for _, msg := range msgs {
		rawBytes := msg.FindBodySection(bodySection)
		if len(rawBytes) == 0 {
			continue
		}

		rawEmail, err := ParseRawEmail(rawBytes)
		if err != nil {
			log.Printf("[IMAP] Failed to parse MIME for UID %d: %v", msg.UID, err)
			continue
		}

		if rawEmail.MessageID != "" {
			processed, err := s.store.HasProcessedEmail(ctx, rawEmail.MessageID)
			if err == nil && processed {
				continue // Already processed
			}
		}

		// Try parsing with bank parsers
		results, err := parser.ParseEmail(rawEmail)
		if err != nil {
			rawEmail.Status = "skipped"
			rawEmail.Error = err.Error()
			_ = s.store.RecordRawEmail(ctx, rawEmail)
			continue
		}

		rawEmail.Status = "parsed"
		if err := s.store.SaveParsedResults(ctx, rawEmail, results); err != nil {
			log.Printf("[IMAP] Failed to store parsed results for %s: %v", rawEmail.MessageID, err)
		} else {
			log.Printf("[IMAP] Successfully parsed %d transaction(s) from email %s (Subject: %q)",
				len(results), rawEmail.MessageID, rawEmail.Subject)
		}
	}

	return nil
}

// IngestRawBytes processes and stores an email directly (used by webhooks/tests).
func IngestRawBytes(ctx context.Context, s *store.Store, rawBytes []byte) ([]models.ParsedResult, error) {
	rawEmail, err := ParseRawEmail(rawBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse email: %w", err)
	}

	if rawEmail.MessageID != "" {
		processed, err := s.HasProcessedEmail(ctx, rawEmail.MessageID)
		if err == nil && processed {
			return nil, fmt.Errorf("email %s already processed", rawEmail.MessageID)
		}
	}

	results, err := parser.ParseEmail(rawEmail)
	if err != nil {
		rawEmail.Status = "skipped"
		rawEmail.Error = err.Error()
		_ = s.RecordRawEmail(ctx, rawEmail)
		return nil, err
	}

	rawEmail.Status = "parsed"
	if err := s.SaveParsedResults(ctx, rawEmail, results); err != nil {
		return nil, fmt.Errorf("failed to save results: %w", err)
	}

	return results, nil
}
