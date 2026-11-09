package http

import (
	"time"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
)

// Request/response bodies. Amounts are integers in minor units
// (e.g. 10050 = 100.50 THB).

type createWalletRequest struct {
	OwnerID  string `json:"owner_id"`
	Currency string `json:"currency"`
}

type amountRequest struct {
	Amount int64 `json:"amount"`
}

type transferRequest struct {
	FromWalletID string `json:"from_wallet_id"`
	ToWalletID   string `json:"to_wallet_id"`
	Amount       int64  `json:"amount"`
}

type walletResponse struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	Currency  string    `json:"currency"`
	Balance   int64     `json:"balance"`
	Status    string    `json:"status"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type entryResponse struct {
	ID            int64     `json:"id"`
	TransactionID string    `json:"transaction_id"`
	WalletID      string    `json:"wallet_id"`
	Amount        int64     `json:"amount"`
	BalanceAfter  int64     `json:"balance_after"`
	CreatedAt     time.Time `json:"created_at"`
}

type transactionResponse struct {
	ID             string          `json:"id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Type           string          `json:"type"`
	Amount         int64           `json:"amount"`
	Currency       string          `json:"currency"`
	FromWalletID   string          `json:"from_wallet_id,omitempty"`
	ToWalletID     string          `json:"to_wallet_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	Entries        []entryResponse `json:"entries"`
}

type balanceEventResponse struct {
	WalletID      string    `json:"wallet_id"`
	TransactionID string    `json:"transaction_id,omitempty"`
	Type          string    `json:"type,omitempty"`
	Amount        int64     `json:"amount"`
	Balance       int64     `json:"balance"`
	Currency      string    `json:"currency"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func toWalletResponse(w *domain.Wallet) walletResponse {
	return walletResponse{
		ID:        w.ID,
		OwnerID:   w.OwnerID,
		Currency:  string(w.Currency),
		Balance:   w.Balance,
		Status:    string(w.Status),
		Version:   w.Version,
		CreatedAt: w.CreatedAt.UTC(),
		UpdatedAt: w.UpdatedAt.UTC(),
	}
}

func toEntryResponses(entries []domain.Entry) []entryResponse {
	out := make([]entryResponse, len(entries))
	for i, e := range entries {
		out[i] = entryResponse{
			ID:            e.ID,
			TransactionID: e.TransactionID,
			WalletID:      e.WalletID,
			Amount:        e.Amount,
			BalanceAfter:  e.BalanceAfter,
			CreatedAt:     e.CreatedAt.UTC(),
		}
	}
	return out
}

func toTransactionResponse(t *domain.Transaction) transactionResponse {
	return transactionResponse{
		ID:             t.ID,
		IdempotencyKey: t.IdempotencyKey,
		Type:           string(t.Type),
		Amount:         t.Amount,
		Currency:       string(t.Currency),
		FromWalletID:   t.FromWalletID,
		ToWalletID:     t.ToWalletID,
		CreatedAt:      t.CreatedAt.UTC(),
		Entries:        toEntryResponses(t.Entries),
	}
}

func toBalanceEventResponse(e domain.BalanceChanged) balanceEventResponse {
	return balanceEventResponse{
		WalletID:      e.WalletID,
		TransactionID: e.TransactionID,
		Type:          string(e.Type),
		Amount:        e.Amount,
		Balance:       e.Balance,
		Currency:      string(e.Currency),
		OccurredAt:    e.OccurredAt.UTC(),
	}
}
