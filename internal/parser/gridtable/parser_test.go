package gridtable

import (
	"strings"
	"testing"
	"time"

	"github.com/NPontious/Bank-Bridge/internal/models"
)

func TestParseDepositAlert(t *testing.T) {
	html := `
	<html>
	<body>
		<h1>Example Credit Union</h1>
		<h2>Large Deposit posted to PREMIER CHECKING *1234</h2>
		<table class="gridtable">
			<tr>
				<th>Transaction Type</th>
				<th>Transaction Amount</th>
				<th>Description</th>
				<th>Date Posted</th>
				<th>Status</th>
			</tr>
			<tr>
				<td>ACH_CREDIT</td>
				<td>$500.00</td>
				<td>Deposit ACME CORP DIRECT DEP</td>
				<td>11/06/2026</td>
				<td>POSTED</td>
			</tr>
		</table>
	</body>
	</html>`

	rawEmail := &models.RawEmail{
		MessageID: "test-deposit-1@example.com",
		Date:      time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
		From:      "alerts@example.com",
		Subject:   "Large Deposit Alert for PREMIER CHECKING",
		BodyHTML:  html,
	}

	parser := NewParser()
	if !parser.CanParse(rawEmail) {
		t.Fatalf("expected CanParse to be true, got false")
	}

	results, err := parser.Parse(rawEmail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	res := results[0]
	if res.Account.Mask != "1234" {
		t.Errorf("expected mask 1234, got %s", res.Account.Mask)
	}
	if !strings.Contains(res.Account.Name, "PREMIER CHECKING") {
		t.Errorf("expected PREMIER CHECKING, got %s", res.Account.Name)
	}
	if res.Account.Institution != "Example Credit Union" {
		t.Errorf("expected institution 'Example Credit Union', got %s", res.Account.Institution)
	}
	if res.Transaction.Amount != 500.00 {
		t.Errorf("expected 500.00, got %f", res.Transaction.Amount)
	}
	if res.Transaction.Type != "income" {
		t.Errorf("expected type income, got %s", res.Transaction.Type)
	}
}

func TestParseDebitAlert(t *testing.T) {
	html := `
	<html>
	<body>
		<h1>Example Credit Union</h1>
		<h2>Debit Card Transaction Alert for PREMIER CHECKING *1234</h2>
		<table class="gridtable">
			<tr>
				<th>Transaction Type</th>
				<th>Transaction Amount</th>
				<th>Description</th>
				<th>Date Posted</th>
				<th>Status</th>
			</tr>
			<tr>
				<td>POS_DEBIT</td>
				<td>$42.50</td>
				<td>Purchase GROCERY STORE #10</td>
				<td>11/07/2026</td>
				<td>POSTED</td>
			</tr>
		</table>
	</body>
	</html>`

	rawEmail := &models.RawEmail{
		MessageID: "test-debit-1@example.com",
		Date:      time.Date(2026, 11, 7, 15, 30, 0, 0, time.UTC),
		From:      "alerts@example.com",
		Subject:   "Debit Card Transaction Alert for PREMIER CHECKING",
		BodyHTML:  html,
	}

	parser := NewParser()
	results, err := parser.Parse(rawEmail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	res := results[0]
	if res.Transaction.Amount != -42.50 {
		t.Errorf("expected debit amount -42.50, got %f", res.Transaction.Amount)
	}
	if res.Transaction.Type != "expense" {
		t.Errorf("expected type expense, got %s", res.Transaction.Type)
	}
	if res.Transaction.Name != "GROCERY STORE #10" {
		t.Errorf("expected cleaned name 'GROCERY STORE #10', got %s", res.Transaction.Name)
	}
}

func TestParseWithdrawalWithMinusSignInTable(t *testing.T) {
	html := `
	<html>
	<body>
		<h1>Example Credit Union</h1>
		<h2>Large Withdrawal posted to SAVINGS *5678</h2>
		<table class="gridtable">
			<tr>
				<th>Transaction Type</th>
				<th>Transaction Amount</th>
				<th>Description</th>
				<th>Date Posted</th>
				<th>Status</th>
			</tr>
			<tr>
				<td>MISCELLANEOUS_DEBIT</td>
				<td>-$150.00</td>
				<td>Withdrawal Transfer To Savings</td>
				<td>11/01/2026</td>
				<td>POSTED</td>
			</tr>
		</table>
	</body>
	</html>`

	rawEmail := &models.RawEmail{
		MessageID: "test-withdraw-1@example.com",
		Date:      time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		From:      "alerts@example.com",
		Subject:   "Large Withdrawal Alert for SAVINGS",
		BodyHTML:  html,
	}

	parser := NewParser()
	results, err := parser.Parse(rawEmail)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	res := results[0]
	if res.Transaction.Amount != -150.00 {
		t.Errorf("expected negative withdrawal amount -150.00, got %f", res.Transaction.Amount)
	}
	if res.Transaction.Type != "expense" {
		t.Errorf("expected type expense, got %s", res.Transaction.Type)
	}
}
