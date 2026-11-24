package store

import (
	"context"
	"testing"
	"time"

	"github.com/NPontious/Bank-Bridge/internal/models"
)

func TestStore(t *testing.T) {
	ctx := context.Background()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory store: %v", err)
	}
	defer s.Close()

	// 1. Test Email Deduplication
	msgID := "test-msg-123"
	processed, err := s.HasProcessedEmail(ctx, msgID)
	if err != nil || processed {
		t.Fatalf("expected processed=false, got %v, err=%v", processed, err)
	}

	email := &models.RawEmail{
		MessageID:   msgID,
		Date:        time.Now().UTC(),
		From:        "alerts@example.com",
		Subject:     "Test Alert",
		BodyHTML:    "<p>Alert</p>",
		ProcessedAt: time.Now().UTC(),
		Status:      "parsed",
	}

	acc := models.Account{
		ID:          "acc-123",
		Name:        "PREMIER CHECKING",
		Mask:        "123",
		Institution: "Example Credit Union",
		Currency:    "USD",
		UpdatedAt:   time.Now().UTC(),
	}

	txn := models.Transaction{
		ID:             "tx-1",
		AccountID:      acc.ID,
		MessageID:      msgID,
		Date:           time.Now().UTC(),
		Amount:         100.50,
		Currency:       "USD",
		Name:           "Deposit Test",
		RawDescription: "Deposit Test Description",
		Type:           "income",
		Status:         "confirmed",
		CreatedAt:      time.Now().UTC(),
	}

	err = s.SaveParsedResults(ctx, email, []models.ParsedResult{
		{Account: acc, Transaction: txn},
	})
	if err != nil {
		t.Fatalf("SaveParsedResults failed: %v", err)
	}

	// 2. Verify HasProcessedEmail is now true
	processed, err = s.HasProcessedEmail(ctx, msgID)
	if err != nil || !processed {
		t.Fatalf("expected processed=true, got %v, err=%v", processed, err)
	}

	// 3. Verify GetAccounts
	accounts, err := s.GetAccounts(ctx)
	if err != nil {
		t.Fatalf("GetAccounts failed: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != "acc-123" {
		t.Fatalf("unexpected accounts: %+v", accounts)
	}

	// 4. Verify GetTransactions
	txns, err := s.GetTransactions(ctx, "acc-123", time.Time{}, 10, 0)
	if err != nil {
		t.Fatalf("GetTransactions failed: %v", err)
	}
	if len(txns) != 1 || txns[0].ID != "tx-1" || txns[0].Amount != 100.50 {
		t.Fatalf("unexpected transactions: %+v", txns)
	}

	// 5. Test UpdateAccountBalance
	bal := 1000.00
	availBal := 1000.00
	err = s.UpdateAccountBalance(ctx, "acc-123", &bal, &availBal)
	if err != nil {
		t.Fatalf("UpdateAccountBalance failed: %v", err)
	}

	gotAcc, err := s.GetAccount(ctx, "acc-123")
	if err != nil || gotAcc == nil {
		t.Fatalf("GetAccount failed: %v", err)
	}
	if gotAcc.Balance == nil || *gotAcc.Balance != 1000.00 {
		t.Fatalf("expected balance 1000.00, got %v", gotAcc.Balance)
	}
	if gotAcc.AvailableBalance == nil || *gotAcc.AvailableBalance != 1000.00 {
		t.Fatalf("expected available_balance 1000.00, got %v", gotAcc.AvailableBalance)
	}

	// 6. Test Running Balance on New Transaction
	txn2 := models.Transaction{
		ID:        "tx-2",
		AccountID: acc.ID,
		Date:      time.Now().UTC(),
		Amount:    50.00,
		Name:      "Another deposit",
		Type:      "income",
		Status:    "confirmed",
		CreatedAt: time.Now().UTC(),
	}
	err = s.SaveParsedResults(ctx, &models.RawEmail{MessageID: "msg-2", Date: time.Now().UTC(), From: "a", Subject: "s"}, []models.ParsedResult{
		{Account: acc, Transaction: txn2},
	})
	if err != nil {
		t.Fatalf("SaveParsedResults with txn2 failed: %v", err)
	}

	gotAcc, err = s.GetAccount(ctx, "acc-123")
	if err != nil || gotAcc == nil {
		t.Fatalf("GetAccount after txn2 failed: %v", err)
	}
	if gotAcc.Balance == nil || *gotAcc.Balance != 1050.00 {
		t.Fatalf("expected running balance 1050.00, got %v", gotAcc.Balance)
	}

	// 7. Test UpsertAccount
	newAcc := &models.Account{
		ID:               "acc-456",
		Name:             "SAVINGS 456",
		Mask:             "456",
		Institution:      "Credit Union",
		Currency:         "USD",
		Balance:          func(f float64) *float64 { return &f }(5000.00),
		AvailableBalance: func(f float64) *float64 { return &f }(4500.00),
	}
	err = s.UpsertAccount(ctx, newAcc)
	if err != nil {
		t.Fatalf("UpsertAccount failed: %v", err)
	}

	gotCert, err := s.GetAccount(ctx, "acc-456")
	if err != nil || gotCert == nil {
		t.Fatalf("GetAccount acc-456 failed: %v", err)
	}
	if gotCert.Balance == nil || *gotCert.Balance != 5000.00 {
		t.Fatalf("expected 5000.00, got %v", gotCert.Balance)
	}
	if gotCert.AvailableBalance == nil || *gotCert.AvailableBalance != 4500.00 {
		t.Fatalf("expected 4500.00, got %v", gotCert.AvailableBalance)
	}

	// 8. Test Balance Summary Save (no transaction, balance update)
	summaryEmail := &models.RawEmail{
		MessageID: "msg-summary-1",
		Date:      time.Now().UTC(),
		From:      "alerts@example.com",
		Subject:   "Weekly Summary",
		Status:    "parsed",
	}
	newBal := 1500.00
	newAvail := 1495.00
	summaryAcc := models.Account{
		ID:               "acc-123",
		Name:             "PREMIER CHECKING",
		Mask:             "123",
		Institution:      "Example Credit Union",
		Currency:         "USD",
		Balance:          &newBal,
		AvailableBalance: &newAvail,
		UpdatedAt:        time.Now().UTC(),
	}
	err = s.SaveParsedResults(ctx, summaryEmail, []models.ParsedResult{
		{Account: summaryAcc, Transaction: models.Transaction{}},
	})
	if err != nil {
		t.Fatalf("SaveParsedResults with summary failed: %v", err)
	}

	gotAcc, err = s.GetAccount(ctx, "acc-123")
	if err != nil || gotAcc == nil {
		t.Fatalf("GetAccount after summary failed: %v", err)
	}
	if gotAcc.Balance == nil || *gotAcc.Balance != 1500.00 {
		t.Fatalf("expected updated balance 1500.00, got %v", gotAcc.Balance)
	}
	if gotAcc.AvailableBalance == nil || *gotAcc.AvailableBalance != 1495.00 {
		t.Fatalf("expected updated available balance 1495.00, got %v", gotAcc.AvailableBalance)
	}

	// 9. Test GetSkippedEmails
	skippedEmail := &models.RawEmail{
		MessageID:   "msg-skipped-1",
		Date:        time.Now().UTC(),
		From:        "alerts@example.com",
		Subject:     "Unknown Format",
		BodyHTML:    "<p>Unknown</p>",
		ProcessedAt: time.Now().UTC(),
		Status:      "skipped",
		Error:       "no parser found",
	}
	if err := s.RecordRawEmail(ctx, skippedEmail); err != nil {
		t.Fatalf("RecordRawEmail failed: %v", err)
	}
	skippedList, err := s.GetSkippedEmails(ctx)
	if err != nil {
		t.Fatalf("GetSkippedEmails failed: %v", err)
	}
	if len(skippedList) != 1 || skippedList[0].MessageID != "msg-skipped-1" {
		t.Fatalf("expected 1 skipped email with msg-skipped-1, got %v", skippedList)
	}
}
