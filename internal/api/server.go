package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NPontious/Bank-Bridge/internal/ingest"
	"github.com/NPontious/Bank-Bridge/internal/models"
	"github.com/NPontious/Bank-Bridge/internal/store"
)

type Server struct {
	store       *store.Store
	imapService *ingest.IMAPService
	apiToken    string
	port        int
	httpSrv     *http.Server
}

func NewServer(s *store.Store, imapSvc *ingest.IMAPService, apiToken string, port int) *Server {
	if port <= 0 {
		port = 8080
	}
	srv := &Server{
		store:       s,
		imapService: imapSvc,
		apiToken:    apiToken,
		port:        port,
	}
	srv.httpSrv = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: srv.Handler(),
	}
	return srv
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public health check
	mux.HandleFunc("GET /health", s.handleHealth)

	// REST API for Sure & custom consumers
	mux.HandleFunc("GET /api/v1/accounts", s.authMiddleware(s.handleGetAccounts))
	mux.HandleFunc("GET /api/v1/accounts/{id}", s.authMiddleware(s.handleGetAccount))
	mux.HandleFunc("POST /api/v1/accounts", s.authMiddleware(s.handleUpsertAccount))
	mux.HandleFunc("POST /api/v1/accounts/{id}/balance", s.authMiddleware(s.handleUpdateAccountBalance))
	mux.HandleFunc("PUT /api/v1/accounts/{id}/balance", s.authMiddleware(s.handleUpdateAccountBalance))
	mux.HandleFunc("PATCH /api/v1/accounts/{id}/balance", s.authMiddleware(s.handleUpdateAccountBalance))
	mux.HandleFunc("GET /api/v1/transactions", s.authMiddleware(s.handleGetTransactions))
	mux.HandleFunc("POST /api/v1/sync", s.authMiddleware(s.handleTriggerSync))
	mux.HandleFunc("POST /api/v1/ingest/email", s.authMiddleware(s.handleIngestEmail))

	// SimpleFIN protocol endpoints
	mux.HandleFunc("POST /claim", s.authMiddleware(s.handleSimpleFINClaim))
	mux.HandleFunc("POST /simplefin/claim", s.authMiddleware(s.handleSimpleFINClaim))
	mux.HandleFunc("GET /accounts", s.authMiddleware(s.handleSimpleFINAccounts))

	return mux
}

func (s *Server) ListenAndServe() error {
	log.Printf("[API] Server listening on %s", s.httpSrv.Addr)
	return s.httpSrv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiToken != "" {
			token := ""
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				// Also check Basic Auth or query param
				_, pass, ok := r.BasicAuth()
				if ok {
					token = pass
				} else {
					token = r.URL.Query().Get("token")
				}
			}

			if subtle.ConstantTimeCompare([]byte(token), []byte(s.apiToken)) != 1 {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		next(w, r)
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleGetAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.store.GetAccounts(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accounts": accounts,
	})
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "account id required")
		return
	}
	account, err := s.store.GetAccount(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if account == nil {
		writeJSONError(w, http.StatusNotFound, "account not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(account)
}

func (s *Server) handleUpsertAccount(w http.ResponseWriter, r *http.Request) {
	var a models.Account
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid json: %s", err.Error()))
		return
	}
	if a.ID == "" {
		if a.Mask != "" {
			a.ID = fmt.Sprintf("acc-%s", a.Mask)
		} else {
			writeJSONError(w, http.StatusBadRequest, "id or mask is required")
			return
		}
	}
	if a.Name == "" {
		a.Name = "Bank Account"
	}
	if a.Institution == "" {
		a.Institution = "Bank"
	}
	if a.Currency == "" {
		a.Currency = "USD"
	}

	if err := s.store.UpsertAccount(r.Context(), &a); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(a)
}

type updateBalanceRequest struct {
	Balance          *float64 `json:"balance"`
	AvailableBalance *float64 `json:"available_balance"`
}

func (s *Server) handleUpdateAccountBalance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "account id required")
		return
	}

	var req updateBalanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid json: %s", err.Error()))
		return
	}

	if req.Balance == nil && req.AvailableBalance == nil {
		writeJSONError(w, http.StatusBadRequest, "balance or available_balance required")
		return
	}

	if req.Balance != nil && req.AvailableBalance == nil {
		req.AvailableBalance = req.Balance
	} else if req.Balance == nil && req.AvailableBalance != nil {
		req.Balance = req.AvailableBalance
	}

	if err := s.store.UpdateAccountBalance(r.Context(), id, req.Balance, req.AvailableBalance); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":            "updated",
		"id":                id,
		"balance":           req.Balance,
		"available_balance": req.AvailableBalance,
	})
}

func (s *Server) handleGetTransactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	accountID := q.Get("account_id")

	var since time.Time
	if sinceStr := q.Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			since = t
		} else if t, err := time.Parse("2006-01-02", sinceStr); err == nil {
			since = t
		}
	}

	limit := 100
	if limitStr := q.Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	offset := 0
	if offsetStr := q.Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	txns, err := s.store.GetTransactions(r.Context(), accountID, since, limit, offset)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"transactions": txns,
		"count":        len(txns),
	})
}

func (s *Server) handleTriggerSync(w http.ResponseWriter, r *http.Request) {
	if s.imapService == nil {
		writeJSONError(w, http.StatusBadRequest, "IMAP service not configured")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.imapService.Sync(ctx); err != nil {
			log.Printf("[API] On-demand sync error: %v", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "sync initiated",
	})
}

func (s *Server) handleIngestEmail(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		writeJSONError(w, http.StatusBadRequest, "empty request body")
		return
	}

	results, err := ingest.IngestRawBytes(r.Context(), s.store, body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "success",
		"imported": len(results),
	})
}

// SimpleFIN protocol data structures
type simpleFinAccount struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Currency         string                 `json:"currency"`
	Balance          string                 `json:"balance"`
	AvailableBalance string                 `json:"available-balance"`
	BalanceDate      int64                  `json:"balance-date"`
	Transactions     []simpleFinTransaction `json:"transactions"`
}

type simpleFinTransaction struct {
	ID          string `json:"id"`
	Posted      int64  `json:"posted"`
	Amount      string `json:"amount"`
	Description string `json:"description"`
	Pending     bool   `json:"pending"`
}

func (s *Server) handleSimpleFINAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.store.GetAccounts(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Optional start-date parameter
	var since time.Time
	if startDateStr := r.URL.Query().Get("start-date"); startDateStr != "" {
		if sec, err := strconv.ParseInt(startDateStr, 10, 64); err == nil {
			since = time.Unix(sec, 0)
		}
	}

	var sfAccounts []simpleFinAccount
	for _, a := range accounts {
		txns, err := s.store.GetTransactions(r.Context(), a.ID, since, 500, 0)
		if err != nil {
			continue
		}

		var sfTxns []simpleFinTransaction
		for _, tx := range txns {
			sfTxns = append(sfTxns, simpleFinTransaction{
				ID:          tx.ID,
				Posted:      tx.Date.Unix(),
				Amount:      fmt.Sprintf("%.2f", tx.Amount),
				Description: tx.Name,
				Pending:     tx.Status == "pending",
			})
		}

		balanceStr := "0.00"
		if a.Balance != nil {
			balanceStr = fmt.Sprintf("%.2f", *a.Balance)
		}
		availStr := balanceStr
		if a.AvailableBalance != nil {
			availStr = fmt.Sprintf("%.2f", *a.AvailableBalance)
		}

		sfAccounts = append(sfAccounts, simpleFinAccount{
			ID:               a.ID,
			Name:             a.Name,
			Currency:         a.Currency,
			Balance:          balanceStr,
			AvailableBalance: availStr,
			BalanceDate:      a.UpdatedAt.Unix(),
			Transactions:     sfTxns,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors":   []string{},
		"accounts": sfAccounts,
	})
}

func (s *Server) handleSimpleFINClaim(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	accessURL := fmt.Sprintf("%s://%s", scheme, r.Host)
	if s.apiToken != "" {
		accessURL = fmt.Sprintf("%s://bridge:%s@%s", scheme, s.apiToken, r.Host)
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(accessURL))
}
