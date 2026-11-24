package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NPontious/Bank-Bridge/internal/models"
	"github.com/NPontious/Bank-Bridge/internal/store"
)

func TestAPIEndpoints(t *testing.T) {
	ctx := context.Background()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}
	defer s.Close()

	// Seed an account and transaction
	acc := models.Account{
		ID:          "acc-123",
		Name:        "PREMIER CHECKING",
		Mask:        "123",
		Institution: "Example Bank",
		Currency:    "USD",
		UpdatedAt:   time.Now().UTC(),
	}
	txn := models.Transaction{
		ID:             "tx-123",
		AccountID:      acc.ID,
		Date:           time.Now().UTC(),
		Amount:         -25.00,
		Currency:       "USD",
		Name:           "Coffee Shop",
		RawDescription: "POS Purchase Coffee Shop",
		Type:           "expense",
		Status:         "confirmed",
		CreatedAt:      time.Now().UTC(),
	}
	email := &models.RawEmail{
		MessageID:   "email-1",
		Date:        time.Now().UTC(),
		From:        "alerts@example.com",
		Subject:     "Debit alert",
		ProcessedAt: time.Now().UTC(),
		Status:      "parsed",
	}
	_ = s.SaveParsedResults(ctx, email, []models.ParsedResult{
		{Account: acc, Transaction: txn},
	})

	server := NewServer(s, nil, "secret-token", 8080)
	handler := server.Handler()

	// 1. Health check (unauthenticated)
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected /health 200, got %d", rr.Code)
	}

	// 2. Unauthorized request to /api/v1/accounts
	req = httptest.NewRequest("GET", "/api/v1/accounts", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected /api/v1/accounts 401, got %d", rr.Code)
	}

	// 3. Authorized request to /api/v1/accounts
	req = httptest.NewRequest("GET", "/api/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected /api/v1/accounts 200, got %d", rr.Code)
	}

	var accResp struct {
		Accounts []models.Account `json:"accounts"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &accResp)
	if len(accResp.Accounts) != 1 || accResp.Accounts[0].ID != "acc-123" {
		t.Fatalf("unexpected accounts response: %+v", accResp)
	}

	// 4. Authorized request to /api/v1/transactions
	req = httptest.NewRequest("GET", "/api/v1/transactions", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected /api/v1/transactions 200, got %d", rr.Code)
	}

	var txnResp struct {
		Transactions []models.Transaction `json:"transactions"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &txnResp)
	if len(txnResp.Transactions) != 1 || txnResp.Transactions[0].Amount != -25.00 {
		t.Fatalf("unexpected transactions response: %+v", txnResp)
	}

	// 5. Test SimpleFIN endpoint /accounts
	req = httptest.NewRequest("GET", "/accounts", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected /accounts 200, got %d", rr.Code)
	}

	var sfResp struct {
		Accounts []simpleFinAccount `json:"accounts"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &sfResp)
	if len(sfResp.Accounts) != 1 || len(sfResp.Accounts[0].Transactions) != 1 {
		t.Fatalf("unexpected SimpleFIN response: %+v", sfResp)
	}
	if sfResp.Accounts[0].Transactions[0].Amount != "-25.00" {
		t.Fatalf("expected SimpleFIN transaction amount '-25.00', got %s", sfResp.Accounts[0].Transactions[0].Amount)
	}

	// 6. Test SimpleFIN setup token claim POST /claim requires authentication when API_TOKEN is set
	req = httptest.NewRequest("POST", "/claim", nil)
	req.Host = "127.0.0.1:8080"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated /claim 401, got %d", rr.Code)
	}

	req = httptest.NewRequest("POST", "/claim?token=secret-token", nil)
	req.Host = "127.0.0.1:8080"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected authenticated /claim 200, got %d", rr.Code)
	}
	expectedAccessURL := "http://bridge:secret-token@127.0.0.1:8080"
	if rr.Body.String() != expectedAccessURL {
		t.Fatalf("expected access URL %q, got %q", expectedAccessURL, rr.Body.String())
	}

	req = httptest.NewRequest("POST", "/simplefin/claim", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected authenticated /simplefin/claim 200, got %d", rr.Code)
	}
	if rr.Body.String() != expectedAccessURL {
		t.Fatalf("expected access URL %q, got %q", expectedAccessURL, rr.Body.String())
	}

	// 7. Test Updating Account Balance via PATCH /api/v1/accounts/acc-123/balance
	balReqBody := `{"balance": 500.00, "available_balance": 450.00}`
	req = httptest.NewRequest("PATCH", "/api/v1/accounts/acc-123/balance", strings.NewReader(balReqBody))
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected PATCH balance 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify SimpleFIN reflects new balances
	req = httptest.NewRequest("GET", "/accounts", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected /accounts 200, got %d", rr.Code)
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &sfResp)
	if sfResp.Accounts[0].Balance != "500.00" || sfResp.Accounts[0].AvailableBalance != "450.00" {
		t.Fatalf("expected SimpleFIN balances 500.00 / 450.00, got %s / %s",
			sfResp.Accounts[0].Balance, sfResp.Accounts[0].AvailableBalance)
	}

	// 8. Test Creating New Account via POST /api/v1/accounts
	newAccBody := `{"id": "acc-456", "name": "SAVINGS 456", "mask": "456", "institution": "Credit Union", "balance": 1000.00, "available_balance": 950.00}`
	req = httptest.NewRequest("POST", "/api/v1/accounts", strings.NewReader(newAccBody))
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected POST /api/v1/accounts 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify GET /api/v1/accounts/acc-456
	req = httptest.NewRequest("GET", "/api/v1/accounts/acc-456", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected GET /api/v1/accounts/acc-456 200, got %d", rr.Code)
	}
	var cert models.Account
	_ = json.Unmarshal(rr.Body.Bytes(), &cert)
	if cert.Balance == nil || *cert.Balance != 1000.00 {
		t.Fatalf("expected account balance 1000.00, got %v", cert.Balance)
	}
}
