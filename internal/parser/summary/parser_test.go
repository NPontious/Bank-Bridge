package summary

import (
	"testing"
	"time"

	"github.com/NPontious/Bank-Bridge/internal/models"
	"github.com/NPontious/Bank-Bridge/internal/parser"
)

func TestParseCheckingSummary(t *testing.T) {
	parser.ResetAccountRules()
	defer parser.ResetAccountRules()

	html := `
	<html>
	<body>
		<h1>Example Credit Union</h1>
		<h2>Balance Update for PREMIER CHECKING *1234</h2>
		<div style="padding:0;margin:0;">
			Your current balance is <strong>$500.00</strong>.
			<br /><br />
			To change or discontinue this alert, log in and change your alert settings.
		</div>
	</body>
	</html>`

	rawEmail := &models.RawEmail{
		MessageID: "test-checking-summary@example.com",
		Date:      time.Date(2026, 11, 11, 13, 40, 0, 0, time.UTC),
		From:      "alerts@example.com",
		Subject:   "Your weekly summary for PREMIER CHECKING",
		BodyHTML:  html,
	}

	p := NewParser()
	if !p.CanParse(rawEmail) {
		t.Fatalf("expected CanParse to be true, got false")
	}

	results, err := p.Parse(rawEmail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	res := results[0]
	if res.Account.ID != "acc-1234" {
		t.Errorf("expected ID acc-1234, got %s", res.Account.ID)
	}
	if res.Account.Name != "PREMIER CHECKING" {
		t.Errorf("expected PREMIER CHECKING, got %s", res.Account.Name)
	}
	if res.Account.Mask != "1234" {
		t.Errorf("expected mask 1234, got %s", res.Account.Mask)
	}
	if *res.Account.Balance != 500.00 {
		t.Errorf("expected balance 500.00, got %f", *res.Account.Balance)
	}
	if *res.Account.AvailableBalance != 500.00 {
		t.Errorf("expected available balance 500.00, got %f", *res.Account.AvailableBalance)
	}
	if res.Transaction.ID != "" {
		t.Errorf("expected empty transaction ID, got %s", res.Transaction.ID)
	}
}

func TestParseSavingsSummary(t *testing.T) {
	parser.ResetAccountRules()
	defer parser.ResetAccountRules()

	html := `
	<html>
	<body>
		<h1>Example Credit Union</h1>
		<h2>Balance Update for PRIMARY SAVINGS *5678</h2>
		<div>
			Your current balance is <strong>$1,000.00</strong>.
		</div>
	</body>
	</html>`

	rawEmail := &models.RawEmail{
		MessageID: "test-savings-summary@example.com",
		Date:      time.Date(2026, 11, 11, 13, 58, 0, 0, time.UTC),
		From:      "alerts@example.com",
		Subject:   "Your weekly summary for PRIMARY SAVINGS",
		BodyHTML:  html,
	}

	p := NewParser()
	if !p.CanParse(rawEmail) {
		t.Fatalf("expected CanParse to be true, got false")
	}

	results, err := p.Parse(rawEmail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := results[0]
	if res.Account.ID != "acc-5678" {
		t.Errorf("expected ID acc-5678, got %s", res.Account.ID)
	}
	if *res.Account.AvailableBalance != 1000.00 {
		t.Errorf("expected available balance 1000.00, got %f", *res.Account.AvailableBalance)
	}
	if *res.Account.Balance != 1000.00 {
		t.Errorf("expected balance 1000.00, got %f", *res.Account.Balance)
	}
}

func TestParseSummaryWithCustomRules(t *testing.T) {
	// Configure custom rule for account mask 9012
	balOffset := 5.00
	availOffset := -50.00
	rulesJSON := `{
		"accounts": {
			"9012": {
				"id": "acc-custom-99",
				"name": "CUSTOM SHARE 99",
				"balance_offset": 5.00,
				"available_offset": -50.00
			}
		}
	}`
	if err := parser.LoadAccountRules(rulesJSON); err != nil {
		t.Fatalf("failed to load account rules: %v", err)
	}
	defer parser.ResetAccountRules()

	html := `
	<html>
	<body>
		<h1>Example Credit Union</h1>
		<h2>Balance Update for TERM SHARE *9012</h2>
		<div>
			Your current balance is <strong>$2,000.00</strong>.
		</div>
	</body>
	</html>`

	rawEmail := &models.RawEmail{
		MessageID: "test-custom-summary@example.com",
		Date:      time.Date(2026, 11, 11, 13, 38, 0, 0, time.UTC),
		From:      "alerts@example.com",
		Subject:   "Your weekly summary for TERM SHARE",
		BodyHTML:  html,
	}

	p := NewParser()
	results, err := p.Parse(rawEmail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := results[0]
	if res.Account.ID != "acc-custom-99" {
		t.Errorf("expected ID acc-custom-99, got %s", res.Account.ID)
	}
	if res.Account.Name != "CUSTOM SHARE 99" {
		t.Errorf("expected Name CUSTOM SHARE 99, got %s", res.Account.Name)
	}
	expectedBalance := 2000.00 + balOffset
	expectedAvailable := 2000.00 + availOffset
	if *res.Account.Balance != expectedBalance {
		t.Errorf("expected balance %f, got %f", expectedBalance, *res.Account.Balance)
	}
	if *res.Account.AvailableBalance != expectedAvailable {
		t.Errorf("expected available balance %f, got %f", expectedAvailable, *res.Account.AvailableBalance)
	}
}
