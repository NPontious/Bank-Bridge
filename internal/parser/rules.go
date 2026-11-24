package parser

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/NPontious/Bank-Bridge/internal/models"
)

// AccountRule defines customizable overrides for account identifiers and balance offsets.
type AccountRule struct {
	ID              string   `json:"id,omitempty"`
	Name            string   `json:"name,omitempty"`
	BalanceOffset   *float64 `json:"balance_offset,omitempty"`
	AvailableOffset *float64 `json:"available_offset,omitempty"`
}

var (
	rulesMu      sync.RWMutex
	accountRules = make(map[string]AccountRule)
)

// SetAccountRules replaces the global account rules map.
func SetAccountRules(rules map[string]AccountRule) {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	accountRules = make(map[string]AccountRule, len(rules))
	for k, v := range rules {
		accountRules[strings.ToLower(strings.TrimSpace(k))] = v
	}
}

// ResetAccountRules clears all global account rules.
func ResetAccountRules() {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	accountRules = make(map[string]AccountRule)
}

// LoadAccountRules parses a JSON string containing account rules.
// Supports both a wrapper object {"accounts": { ... }} and a direct map {"<key>": { ... }}.
func LoadAccountRules(jsonStr string) error {
	trimmed := strings.TrimSpace(jsonStr)
	if trimmed == "" {
		ResetAccountRules()
		return nil
	}

	// Try wrapper object {"accounts": { ... }} first
	var wrapper struct {
		Accounts map[string]AccountRule `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrapper); err == nil && len(wrapper.Accounts) > 0 {
		SetAccountRules(wrapper.Accounts)
		return nil
	}

	// Fallback to direct map
	var direct map[string]AccountRule
	if err := json.Unmarshal([]byte(trimmed), &direct); err != nil {
		return err
	}
	SetAccountRules(direct)
	return nil
}

// ApplyAccountRules updates the provided account using any matching configured rule.
// Matches by account mask, ID, or name (case-insensitive).
func ApplyAccountRules(account *models.Account) {
	if account == nil {
		return
	}

	rulesMu.RLock()
	defer rulesMu.RUnlock()

	if len(accountRules) == 0 {
		return
	}

	var matchedRule *AccountRule

	// 1. Try exact mask match (e.g. "1234", "5678")
	if account.Mask != "" {
		if r, ok := accountRules[strings.ToLower(account.Mask)]; ok {
			matchedRule = &r
		}
	}

	// 2. Try exact ID match (e.g. "acc-1234", "acc-5678")
	if matchedRule == nil && account.ID != "" {
		if r, ok := accountRules[strings.ToLower(account.ID)]; ok {
			matchedRule = &r
		}
	}

	// 3. Try name match or substring match
	if matchedRule == nil && account.Name != "" {
		lowerName := strings.ToLower(account.Name)
		if r, ok := accountRules[lowerName]; ok {
			matchedRule = &r
		} else {
			for key, r := range accountRules {
				if strings.Contains(lowerName, key) {
					ruleCopy := r
					matchedRule = &ruleCopy
					break
				}
			}
		}
	}

	if matchedRule == nil {
		return
	}

	if matchedRule.ID != "" {
		account.ID = matchedRule.ID
	}
	if matchedRule.Name != "" {
		account.Name = matchedRule.Name
	}
	if matchedRule.BalanceOffset != nil && account.Balance != nil {
		*account.Balance += *matchedRule.BalanceOffset
	}
	if matchedRule.AvailableOffset != nil && account.AvailableBalance != nil {
		*account.AvailableBalance += *matchedRule.AvailableOffset
	}
}
