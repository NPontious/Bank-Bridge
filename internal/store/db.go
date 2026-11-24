package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NPontious/Bank-Bridge/internal/models"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil && filepath.Dir(dbPath) != "." {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	query := `
	CREATE TABLE IF NOT EXISTS accounts (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		mask TEXT,
		institution TEXT NOT NULL,
		currency TEXT NOT NULL DEFAULT 'USD',
		balance REAL,
		available_balance REAL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS transactions (
		id TEXT PRIMARY KEY,
		account_id TEXT NOT NULL REFERENCES accounts(id),
		message_id TEXT,
		date DATETIME NOT NULL,
		amount REAL NOT NULL,
		currency TEXT NOT NULL DEFAULT 'USD',
		name TEXT NOT NULL,
		raw_description TEXT,
		type TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'confirmed',
		created_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_transactions_account_date ON transactions(account_id, date DESC);
	CREATE INDEX IF NOT EXISTS idx_transactions_date ON transactions(date DESC);

	CREATE TABLE IF NOT EXISTS raw_emails (
		message_id TEXT PRIMARY KEY,
		date DATETIME NOT NULL,
		from_addr TEXT NOT NULL,
		subject TEXT NOT NULL,
		body_html TEXT,
		body_text TEXT,
		processed_at DATETIME NOT NULL,
		status TEXT NOT NULL,
		error TEXT
	);
	`
	if _, err := s.db.Exec(query); err != nil {
		return err
	}
	// Migrate existing tables if available_balance column does not exist
	_, _ = s.db.Exec("ALTER TABLE accounts ADD COLUMN available_balance REAL")

	// Fix any historical transactions that were inverted due to double-negative amount parsing
	_, _ = s.db.Exec(`
		UPDATE transactions 
		SET amount = -ABS(amount) 
		WHERE (type = 'expense' OR raw_description LIKE '%Withdrawal%' OR raw_description LIKE '%Debit%') 
		  AND amount > 0
	`)
	_, _ = s.db.Exec(`
		UPDATE transactions 
		SET amount = ABS(amount) 
		WHERE type = 'income' AND amount < 0
	`)

	return nil
}

func (s *Store) HasProcessedEmail(ctx context.Context, messageID string) (bool, error) {
	if messageID == "" {
		return false, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM raw_emails WHERE message_id = ?", messageID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) RecordRawEmail(ctx context.Context, email *models.RawEmail) error {
	query := `
	INSERT INTO raw_emails (message_id, date, from_addr, subject, body_html, body_text, processed_at, status, error)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(message_id) DO UPDATE SET
		processed_at = excluded.processed_at,
		status = excluded.status,
		error = excluded.error
	`
	_, err := s.db.ExecContext(ctx, query,
		email.MessageID,
		email.Date.UTC(),
		email.From,
		email.Subject,
		email.BodyHTML,
		email.BodyText,
		email.ProcessedAt.UTC(),
		email.Status,
		email.Error,
	)
	return err
}

func (s *Store) SaveParsedResults(ctx context.Context, email *models.RawEmail, results []models.ParsedResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Record Email
	recordEmailQuery := `
	INSERT INTO raw_emails (message_id, date, from_addr, subject, body_html, body_text, processed_at, status, error)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(message_id) DO UPDATE SET
		processed_at = excluded.processed_at,
		status = excluded.status,
		error = excluded.error
	`
	_, err = tx.ExecContext(ctx, recordEmailQuery,
		email.MessageID,
		email.Date.UTC(),
		email.From,
		email.Subject,
		email.BodyHTML,
		email.BodyText,
		email.ProcessedAt.UTC(),
		email.Status,
		email.Error,
	)
	if err != nil {
		return fmt.Errorf("failed to save raw email: %w", err)
	}

	// 2. Upsert Accounts & Transactions
	accountQuery := `
	INSERT INTO accounts (id, name, mask, institution, currency, balance, available_balance, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name = excluded.name,
		mask = excluded.mask,
		institution = excluded.institution,
		balance = COALESCE(excluded.balance, accounts.balance),
		available_balance = COALESCE(excluded.available_balance, accounts.available_balance),
		updated_at = excluded.updated_at
	`

	txnQuery := `
	INSERT INTO transactions (id, account_id, message_id, date, amount, currency, name, raw_description, type, status, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO NOTHING
	`

	balanceUpdateQuery := `
	UPDATE accounts
	SET balance = balance + ?,
		available_balance = CASE WHEN available_balance IS NOT NULL THEN available_balance + ? ELSE balance + ? END,
		updated_at = ?
	WHERE id = ? AND balance IS NOT NULL
	`

	for _, res := range results {
		_, err := tx.ExecContext(ctx, accountQuery,
			res.Account.ID,
			res.Account.Name,
			res.Account.Mask,
			res.Account.Institution,
			res.Account.Currency,
			res.Account.Balance,
			res.Account.AvailableBalance,
			res.Account.UpdatedAt.UTC(),
		)
		if err != nil {
			return fmt.Errorf("failed to save account %s: %w", res.Account.ID, err)
		}

		if res.Transaction.ID != "" {
			resTxn, err := tx.ExecContext(ctx, txnQuery,
				res.Transaction.ID,
				res.Transaction.AccountID,
				res.Transaction.MessageID,
				res.Transaction.Date.UTC(),
				res.Transaction.Amount,
				res.Transaction.Currency,
				res.Transaction.Name,
				res.Transaction.RawDescription,
				res.Transaction.Type,
				res.Transaction.Status,
				res.Transaction.CreatedAt.UTC(),
			)
			if err != nil {
				return fmt.Errorf("failed to save transaction %s: %w", res.Transaction.ID, err)
			}

			// If a new transaction was inserted, update the running balance if tracked
			if rows, _ := resTxn.RowsAffected(); rows > 0 {
				_, _ = tx.ExecContext(ctx, balanceUpdateQuery,
					res.Transaction.Amount,
					res.Transaction.Amount,
					res.Transaction.Amount,
					res.Transaction.Date.UTC(),
					res.Transaction.AccountID,
				)
			}
		}
	}

	return tx.Commit()
}

func (s *Store) GetAccounts(ctx context.Context) ([]models.Account, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, mask, institution, currency, balance, available_balance, updated_at FROM accounts ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []models.Account
	for rows.Next() {
		var a models.Account
		var mask sql.NullString
		var balance, availBalance sql.NullFloat64
		var updatedAt time.Time

		if err := rows.Scan(&a.ID, &a.Name, &mask, &a.Institution, &a.Currency, &balance, &availBalance, &updatedAt); err != nil {
			return nil, err
		}
		if mask.Valid {
			a.Mask = mask.String
		}
		if balance.Valid {
			a.Balance = &balance.Float64
		}
		if availBalance.Valid {
			a.AvailableBalance = &availBalance.Float64
		}
		a.UpdatedAt = updatedAt
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (s *Store) GetAccount(ctx context.Context, id string) (*models.Account, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, name, mask, institution, currency, balance, available_balance, updated_at FROM accounts WHERE id = ?", id)
	var a models.Account
	var mask sql.NullString
	var balance, availBalance sql.NullFloat64
	var updatedAt time.Time

	if err := row.Scan(&a.ID, &a.Name, &mask, &a.Institution, &a.Currency, &balance, &availBalance, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if mask.Valid {
		a.Mask = mask.String
	}
	if balance.Valid {
		a.Balance = &balance.Float64
	}
	if availBalance.Valid {
		a.AvailableBalance = &availBalance.Float64
	}
	a.UpdatedAt = updatedAt
	return &a, nil
}

func (s *Store) UpdateAccountBalance(ctx context.Context, id string, balance *float64, availableBalance *float64) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, "UPDATE accounts SET balance = ?, available_balance = ?, updated_at = ? WHERE id = ?", balance, availableBalance, now, id)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("account not found: %s", id)
	}
	return nil
}

func (s *Store) UpsertAccount(ctx context.Context, a *models.Account) error {
	now := time.Now().UTC()
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = now
	}
	if a.Currency == "" {
		a.Currency = "USD"
	}
	query := `
	INSERT INTO accounts (id, name, mask, institution, currency, balance, available_balance, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name = excluded.name,
		mask = excluded.mask,
		institution = excluded.institution,
		currency = excluded.currency,
		balance = COALESCE(excluded.balance, accounts.balance),
		available_balance = COALESCE(excluded.available_balance, accounts.available_balance),
		updated_at = excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		a.ID,
		a.Name,
		a.Mask,
		a.Institution,
		a.Currency,
		a.Balance,
		a.AvailableBalance,
		a.UpdatedAt.UTC(),
	)
	return err
}

func (s *Store) GetTransactions(ctx context.Context, accountID string, since time.Time, limit int, offset int) ([]models.Transaction, error) {
	baseQuery := "SELECT id, account_id, message_id, date, amount, currency, name, raw_description, type, status, created_at FROM transactions WHERE 1=1"
	var args []any

	if accountID != "" {
		baseQuery += " AND account_id = ?"
		args = append(args, accountID)
	}
	if !since.IsZero() {
		baseQuery += " AND date >= ?"
		args = append(args, since.UTC())
	}

	baseQuery += " ORDER BY date DESC"

	if limit > 0 {
		baseQuery += " LIMIT ?"
		args = append(args, limit)
		if offset > 0 {
			baseQuery += " OFFSET ?"
			args = append(args, offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txns []models.Transaction
	for rows.Next() {
		var tx models.Transaction
		var msgID, rawDesc sql.NullString
		var txDate, createdAt time.Time

		if err := rows.Scan(
			&tx.ID, &tx.AccountID, &msgID, &txDate, &tx.Amount, &tx.Currency,
			&tx.Name, &rawDesc, &tx.Type, &tx.Status, &createdAt,
		); err != nil {
			return nil, err
		}
		if msgID.Valid {
			tx.MessageID = msgID.String
		}
		if rawDesc.Valid {
			tx.RawDescription = rawDesc.String
		}
		tx.Date = txDate
		tx.CreatedAt = createdAt
		txns = append(txns, tx)
	}
	return txns, rows.Err()
}

// GetSkippedEmails returns all raw emails that were previously marked skipped.
func (s *Store) GetSkippedEmails(ctx context.Context) ([]models.RawEmail, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, date, from_addr, subject, body_html, body_text, processed_at, status, error 
		FROM raw_emails 
		WHERE status = 'skipped' 
		ORDER BY date ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var emails []models.RawEmail
	for rows.Next() {
		var em models.RawEmail
		var bodyHTML, bodyText, errStr sql.NullString
		if err := rows.Scan(
			&em.MessageID,
			&em.Date,
			&em.From,
			&em.Subject,
			&bodyHTML,
			&bodyText,
			&em.ProcessedAt,
			&em.Status,
			&errStr,
		); err != nil {
			return nil, err
		}
		if bodyHTML.Valid {
			em.BodyHTML = bodyHTML.String
		}
		if bodyText.Valid {
			em.BodyText = bodyText.String
		}
		if errStr.Valid {
			em.Error = errStr.String
		}
		emails = append(emails, em)
	}
	return emails, rows.Err()
}

