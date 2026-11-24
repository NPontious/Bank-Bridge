package models

import "time"

// Account represents a bank account extracted from alerts or configured.
type Account struct {
	ID          string    `json:"id"`                    // e.g. "acc-1234"
	Name        string    `json:"name"`                  // e.g. "PRIMARY CHECKING"
	Mask        string    `json:"mask,omitempty"`        // e.g. "1234"
	Institution      string    `json:"institution"`                 // e.g. "My Credit Union"
	Currency         string    `json:"currency"`                    // e.g. "USD"
	Balance          *float64  `json:"balance,omitempty"`           // Available if provided in email or set
	AvailableBalance *float64  `json:"available_balance,omitempty"` // Available balance
	UpdatedAt        time.Time `json:"updated_at"`
}

// Transaction represents a normalized financial transaction.
type Transaction struct {
	ID             string    `json:"id"`                        // Unique deterministic ID or UUID
	AccountID      string    `json:"account_id"`                // Matches Account.ID
	MessageID      string    `json:"message_id,omitempty"`      // Source email Message-ID
	Date           time.Time `json:"date"`                      // Posted or transaction timestamp
	Amount         float64   `json:"amount"`                    // Positive for income/credit, negative for expense/debit
	Currency       string    `json:"currency"`                  // e.g. "USD"
	Name           string    `json:"name"`                      // Clean merchant/payer name
	RawDescription string    `json:"raw_description,omitempty"` // Full raw description
	Type           string    `json:"type"`                      // "income", "expense", "transfer"
	Status         string    `json:"status"`                    // "confirmed" or "pending"
	CreatedAt      time.Time `json:"created_at"`
}

// RawEmail stores metadata and raw content of an ingested email.
type RawEmail struct {
	MessageID   string    `json:"message_id"`
	Date        time.Time `json:"date"`
	From        string    `json:"from"`
	Subject     string    `json:"subject"`
	BodyHTML    string    `json:"body_html,omitempty"`
	BodyText    string    `json:"body_text,omitempty"`
	ProcessedAt time.Time `json:"processed_at"`
	Status      string    `json:"status"` // "parsed", "skipped", "failed"
	Error       string    `json:"error,omitempty"`
}

// ParsedResult pairs an account and transaction extracted from an alert email.
type ParsedResult struct {
	Account     Account
	Transaction Transaction
}
