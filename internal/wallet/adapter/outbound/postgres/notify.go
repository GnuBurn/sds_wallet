package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

// BalanceChannel is the Postgres NOTIFY channel for balance events.
const BalanceChannel = "wallet_balance_changed"

// balanceChangedMsg is the wire format of a balance event.
type balanceChangedMsg struct {
	WalletID      string    `json:"wallet_id"`
	TransactionID string    `json:"transaction_id"`
	Type          string    `json:"type"`
	Amount        int64     `json:"amount"`
	Balance       int64     `json:"balance"`
	Currency      string    `json:"currency"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// NotifyPublisher publishes events with pg_notify. When q is a transaction,
// Postgres only delivers the notification if the transaction commits, so
// subscribers never see balances that were rolled back.
//
// Later this can be replaced by an outbox writer + Kafka/NATS publisher
// without changing the core.
type NotifyPublisher struct {
	q querier
}

var _ port.EventPublisher = (*NotifyPublisher)(nil)

func NewNotifyPublisher(q querier) *NotifyPublisher {
	return &NotifyPublisher{q: q}
}

func (p *NotifyPublisher) PublishBalanceChanged(ctx context.Context, e domain.BalanceChanged) error {
	payload, err := json.Marshal(balanceChangedMsg{
		WalletID:      e.WalletID,
		TransactionID: e.TransactionID,
		Type:          string(e.Type),
		Amount:        e.Amount,
		Balance:       e.Balance,
		Currency:      string(e.Currency),
		OccurredAt:    e.OccurredAt,
	})
	if err != nil {
		return err
	}
	_, err = p.q.Exec(ctx, "SELECT pg_notify($1, $2)", BalanceChannel, string(payload))
	return err
}
