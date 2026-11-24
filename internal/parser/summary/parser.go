package summary

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/NPontious/Bank-Bridge/internal/models"
	"github.com/NPontious/Bank-Bridge/internal/parser"
)

func init() {
	parser.Register(NewParser())
}

// Parser parses HTML email alerts formatted as account balance updates / summaries.
type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) ID() string {
	return "summary"
}

func (p *Parser) Name() string {
	return "Account Summary and Balance Alert Parser"
}

// CanParse checks if the email is a balance update / summary notification.
func (p *Parser) CanParse(email *models.RawEmail) bool {
	body := strings.ToLower(email.BodyHTML)
	subject := strings.ToLower(email.Subject)

	hasSummaryMarker := strings.Contains(subject, "summary") ||
		strings.Contains(subject, "balance") ||
		strings.Contains(body, "balance update")
	hasBalanceText := strings.Contains(body, "your current balance is")

	return hasSummaryMarker && hasBalanceText
}

var (
	// Matches patterns like "Balance Update for PREMIER CHECKING *1234" or "PRIMARY SAVINGS *5678"
	accountRegex = regexp.MustCompile(`([A-Z0-9\s]+?)\s*\*(\d{2,4})`)
	// Matches patterns like "Your current balance is <strong>$500.00</strong>."
	balanceRegex = regexp.MustCompile(`(?i)your\s+current\s+balance\s+is\s*<strong>\s*\$?([0-9,]+\.[0-9]{2})\s*</strong>`)
)

// Parse extracts account balances from the balance summary email.
func (p *Parser) Parse(email *models.RawEmail) ([]models.ParsedResult, error) {
	if email.BodyHTML == "" {
		return nil, fmt.Errorf("email body is empty or not HTML")
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(email.BodyHTML))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML document: %w", err)
	}

	// 1. Extract Institution Name from <h1>
	institution := strings.TrimSpace(doc.Find("h1").Text())
	if institution == "" {
		institution = "Bank"
	}

	// 2. Extract Account Name & Mask
	accountName := "Bank Account"
	accountMask := ""
	accountID := "acc-default"

	headingText := strings.TrimSpace(doc.Find("h2").Text())
	match := accountRegex.FindStringSubmatch(headingText)

	if len(match) < 3 {
		doc.Find("strong").EachWithBreak(func(i int, s *goquery.Selection) bool {
			m := accountRegex.FindStringSubmatch(s.Text())
			if len(m) >= 3 {
				match = m
				return false
			}
			return true
		})
	}

	if len(match) >= 3 {
		accountName = cleanAccountName(match[1])
		accountMask = match[2]
		accountID = fmt.Sprintf("acc-%s", accountMask)
	}

	// 3. Extract Balance Amount
	mBal := balanceRegex.FindStringSubmatch(email.BodyHTML)
	if len(mBal) < 2 {
		return nil, fmt.Errorf("failed to find 'your current balance is' amount in email")
	}

	cleanAmt := strings.ReplaceAll(mBal[1], ",", "")
	amtVal, err := strconv.ParseFloat(cleanAmt, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse balance amount %q: %w", mBal[1], err)
	}

	balanceVal := amtVal
	availVal := amtVal

	account := models.Account{
		ID:               accountID,
		Name:             accountName,
		Mask:             accountMask,
		Institution:      institution,
		Currency:         "USD",
		Balance:          &balanceVal,
		AvailableBalance: &availVal,
		UpdatedAt:        email.Date,
	}

	// Apply any optional user-configured account rules (hold offsets, ID overrides)
	parser.ApplyAccountRules(&account)

	return []models.ParsedResult{
		{
			Account:     account,
			Transaction: models.Transaction{}, // No transaction for balance summary alert
		},
	}, nil
}

func cleanAccountName(name string) string {
	name = strings.TrimSpace(name)
	name = regexp.MustCompile(`(?i)^(?:balance\s+update\s+for\s+|posted\s+to\s+|to\s+)`).ReplaceAllString(name, "")
	return strings.TrimSpace(name)
}
