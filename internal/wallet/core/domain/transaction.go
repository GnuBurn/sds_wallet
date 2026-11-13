package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// MaxNoteLength is the maximum number of characters in a transfer note.
const MaxNoteLength = 80

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
	Note           string // optional, free text from the sender (transfers only)
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

// HistoryEntry is a ledger entry enriched with details of its transaction,
// used to render a wallet's history.
type HistoryEntry struct {
	Entry
	Type                 TransactionType
	Note                 string
	CounterpartyWalletID string // the other wallet of a transfer; empty otherwise
	CounterpartyOwnerID  string
}

// EntryDirection filters history by the sign of the entry amount.
type EntryDirection string

const (
	DirectionAll EntryDirection = ""
	DirectionIn  EntryDirection = "in"
	DirectionOut EntryDirection = "out"
)

// NormalizeNote trims a note and checks its length.
func NormalizeNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > MaxNoteLength {
		return "", ErrNoteTooLong
	}
	return note, nil
}

// SameRequest reports whether t describes the same operation as other.
// Used to decide whether a reused idempotency key is a safe replay.
func (t *Transaction) SameRequest(other *Transaction) bool {
	return t.Type == other.Type &&
		t.Amount == other.Amount &&
		t.FromWalletID == other.FromWalletID &&
		t.ToWalletID == other.ToWalletID &&
		t.Note == other.Note
}
