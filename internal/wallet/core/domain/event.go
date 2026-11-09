package domain

import "time"

// BalanceChanged is emitted once per wallet affected by a committed transaction.
// It carries the new balance so subscribers never need to re-query.
type BalanceChanged struct {
	WalletID      string
	TransactionID string
	Type          TransactionType
	Amount        int64 // signed change applied to this wallet
	Balance       int64 // balance after the change
	Currency      Currency
	OccurredAt    time.Time
}
