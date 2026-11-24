package gridtable

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/NPontious/Bank-Bridge/internal/models"
	"github.com/NPontious/Bank-Bridge/internal/parser"
)

func init() {
	parser.Register(NewParser())
}

// Parser parses HTML email alerts formatted with standard transaction grid tables.
type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) ID() string {
	return "gridtable"
}

func (p *Parser) Name() string {
	return "GridTable Financial Alert Parser"
}

// CanParse checks if the email has the signature transaction grid table.
func (p *Parser) CanParse(email *models.RawEmail) bool {
	body := strings.ToLower(email.BodyHTML)
	subject := strings.ToLower(email.Subject)

	hasTable := strings.Contains(body, "gridtable")
	hasAlertMarker := strings.Contains(subject, "alert") ||
		strings.Contains(body, "transaction") ||
		strings.Contains(body, "deposit") ||
		strings.Contains(body, "withdrawal")

	return hasTable && hasAlertMarker
}

var (
	// Matches patterns like "MY CHECKING *123" or "SHARE SAVINGS *456" or "SHARE CERTIFICATE *30"
	accountRegex = regexp.MustCompile(`([A-Z0-9\s]+?)\s*\*(\d{2,4})`)
	// Cleans currency values like "$1,234.56"
	currencyCleaner = regexp.MustCompile(`[^0-9.-]`)
)

// Parse extracts accounts and transactions from the alert email.
func (p *Parser) Parse(email *models.RawEmail) ([]models.ParsedResult, error) {
	if email.BodyHTML == "" {
		return nil, fmt.Errorf("email body is empty or not HTML")
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(email.BodyHTML))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML document: %w", err)
	}

	// 1. Extract Institution Name from <h1> (e.g. Bank / Credit Union header)
	institution := strings.TrimSpace(doc.Find("h1").Text())
	if institution == "" {
		institution = "Bank"
	}

	// 2. Extract Account Name & Mask
	accountName := "Bank Account"
	accountMask := ""
	accountID := "acc-default"

	// Check h2 first (e.g. "Large Deposit posted to PREMIER CHECKING *1234")
	headingText := strings.TrimSpace(doc.Find("h2").Text())
	match := accountRegex.FindStringSubmatch(headingText)

	// If not in h2, check <strong> elements
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

	account := models.Account{
		ID:          accountID,
		Name:        accountName,
		Mask:        accountMask,
		Institution: institution,
		Currency:    "USD",
		UpdatedAt:   email.Date,
	}

	// Apply any optional user-configured account rules (hold offsets, ID overrides)
	parser.ApplyAccountRules(&account)
	accountID = account.ID

	// 3. Parse the Grid Table
	var results []models.ParsedResult
	table := doc.Find("table.gridtable")
	if table.Length() == 0 {
		return nil, fmt.Errorf("no alert gridtable found in email")
	}

	// Map headers to column indices
	headerMap := make(map[string]int)
	table.Find("tr").First().Find("th").Each(func(i int, th *goquery.Selection) {
		key := strings.ToLower(strings.TrimSpace(th.Text()))
		headerMap[key] = i
	})

	// Iterate rows
	table.Find("tr").Slice(1, goquery.ToEnd).Each(func(rowIndex int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() == 0 {
			return
		}

		getVal := func(columnKey string) string {
			for header, idx := range headerMap {
				if strings.Contains(header, columnKey) {
					if idx < cells.Length() {
						return strings.TrimSpace(cells.Eq(idx).Text())
					}
				}
			}
			return ""
		}

		rawType := getVal("type")     // e.g. "ACH_CREDIT", "ACH_DEBIT", "POS_DEBIT"
		rawAmount := getVal("amount") // e.g. "$123.45"
		rawDesc := getVal("desc")     // e.g. "Deposit DIRECT PAY"
		rawDate := getVal("date")     // e.g. "11/06/2026"
		rawStatus := getVal("status") // e.g. "POSTED"

		if rawAmount == "" {
			return
		}

		// Clean and parse amount
		cleanAmtStr := currencyCleaner.ReplaceAllString(rawAmount, "")
		amtVal, err := strconv.ParseFloat(cleanAmtStr, 64)
		if err != nil {
			return
		}

		// Ensure proper sign regardless of whether raw amount has - or +
		amtVal = math.Abs(amtVal)
		isCredit := determineIsCredit(rawType, email.Subject)
		txType := "expense"
		if isCredit {
			txType = "income"
		} else {
			amtVal = -amtVal
		}

		if strings.Contains(strings.ToLower(rawType), "transfer") {
			txType = "transfer"
		}

		// Parse date
		txDate := email.Date
		if rawDate != "" {
			if parsedDate, err := time.Parse("01/02/2006", rawDate); err == nil {
				txDate = time.Date(
					parsedDate.Year(), parsedDate.Month(), parsedDate.Day(),
					email.Date.Hour(), email.Date.Minute(), email.Date.Second(),
					0, email.Date.Location(),
				)
			}
		}

		// Status
		status := "confirmed"
		if strings.ToUpper(rawStatus) == "PENDING" {
			status = "pending"
		}

		// Clean payee name
		cleanName := cleanMerchantName(rawDesc, rawType)

		// Deterministic transaction ID
		txHash := computeTransactionID(accountID, rawDate, cleanAmtStr, rawDesc, email.MessageID, rowIndex)

		tx := models.Transaction{
			ID:             txHash,
			AccountID:      accountID,
			MessageID:      email.MessageID,
			Date:           txDate,
			Amount:         amtVal,
			Currency:       "USD",
			Name:           cleanName,
			RawDescription: rawDesc,
			Type:           txType,
			Status:         status,
			CreatedAt:      time.Now().UTC(),
		}

		results = append(results, models.ParsedResult{
			Account:     account,
			Transaction: tx,
		})
	})

	if len(results) == 0 {
		return nil, fmt.Errorf("no transactions extracted from gridtable")
	}

	return results, nil
}

func determineIsCredit(rawType, subject string) bool {
	upperType := strings.ToUpper(rawType)
	upperSubj := strings.ToUpper(subject)

	if strings.Contains(upperType, "CREDIT") || strings.Contains(upperType, "DEPOSIT") {
		return true
	}
	if strings.Contains(upperType, "DEBIT") || strings.Contains(upperType, "WITHDRAWAL") || strings.Contains(upperType, "CHECK") {
		return false
	}
	if strings.Contains(upperSubj, "DEPOSIT") {
		return true
	}
	return false
}

func cleanAccountName(name string) string {
	name = strings.TrimSpace(name)
	name = regexp.MustCompile(`(?i)^(?:posted\s+to\s+|to\s+)`).ReplaceAllString(name, "")
	return strings.TrimSpace(name)
}

func cleanMerchantName(rawDesc, rawType string) string {
	desc := strings.TrimSpace(rawDesc)
	desc = regexp.MustCompile(`(?i)^(?:Deposit\s+|Withdrawal\s+|Purchase\s+|POS\s+|ACH\s+)`).ReplaceAllString(desc, "")
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return rawType
	}
	return desc
}

func computeTransactionID(accountID, date, amount, desc, messageID string, rowIdx int) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%d", accountID, date, amount, desc, messageID, rowIdx)))
	return "tx-" + hex.EncodeToString(h.Sum(nil))[:16]
}
