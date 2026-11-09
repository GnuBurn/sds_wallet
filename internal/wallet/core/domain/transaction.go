package domain

import "time"

type TransactionType string

const (
	TxDeposit    TransactionType = "deposit"
	TxWithdrawal TransactionType = "withdrawal"
	TxTransfer   TransactionType = "transfer"
)

// Transaction is one business operation. Its effect on balances is recorded
// as append-only ledger Entries (one per affected wallet).
type Transaction struct {
	ID             string
	IdempotencyKey string
	Type           TransactionType
	Amount         int64 // always positive
	Currency       Currency
	FromWalletID   string // empty for deposits
	ToWalletID     string // empty for withdrawals
	CreatedAt      time.Time
	Entries        []Entry
}

// Entry is a single ledger line. Amount is signed: positive = credit,
// negative = debit. BalanceAfter is the wallet balance right after this entry.
type Entry struct {
	ID            int64
	TransactionID string
	WalletID      string
	Amount        int64
	BalanceAfter  int64
	CreatedAt     time.Time
}

// SameRequest reports whether t describes the same operation as other.
// Used to decide whether a reused idempotency key is a safe replay.
func (t *Transaction) SameRequest(other *Transaction) bool {
	return t.Type == other.Type &&
		t.Amount == other.Amount &&
		t.FromWalletID == other.FromWalletID &&
		t.ToWalletID == other.ToWalletID
}
