package parser

import (
	"fmt"
	"sync"

	"github.com/NPontious/Bank-Bridge/internal/models"
)

// Parser is the interface implemented by bank-specific email parsers.
type Parser interface {
	// ID returns a unique short identifier for the parser (e.g. "gridtable").
	ID() string
	// Name returns a human-readable name of the bank/institution.
	Name() string
	// CanParse checks if the incoming email matches this parser's format.
	CanParse(email *models.RawEmail) bool
	// Parse extracts accounts and transactions from the email.
	Parse(email *models.RawEmail) ([]models.ParsedResult, error)
}

// Registry manages all registered bank parsers.
type Registry struct {
	mu      sync.RWMutex
	parsers []Parser
}

// Global registry instance.
var defaultRegistry = &Registry{}

// Register registers a new parser to the default registry.
func Register(p Parser) {
	defaultRegistry.Register(p)
}

// Register adds a parser to the registry.
func (r *Registry) Register(p Parser) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parsers = append(r.parsers, p)
}

// ParseEmail finds the first matching parser and parses the email.
func ParseEmail(email *models.RawEmail) ([]models.ParsedResult, error) {
	return defaultRegistry.ParseEmail(email)
}

// ParseEmail iterates registered parsers and runs the first matching one.
func (r *Registry) ParseEmail(email *models.RawEmail) ([]models.ParsedResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.parsers {
		if p.CanParse(email) {
			results, err := p.Parse(email)
			if err != nil {
				return nil, fmt.Errorf("parser %q failed: %w", p.ID(), err)
			}
			return results, nil
		}
	}

	return nil, fmt.Errorf("no matching parser found for email from %q with subject %q", email.From, email.Subject)
}

// Parsers returns a copy of registered parsers.
func (r *Registry) Parsers() []Parser {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Parser, len(r.parsers))
	copy(out, r.parsers)
	return out
}
