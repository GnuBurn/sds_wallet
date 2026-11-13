package port

import (
	"context"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
)

// WalletService is the inbound (driving) port: the use cases exposed to
// adapters such as the HTTP handler.
//
// Methods that take a userID act on behalf of that user and return
// domain.ErrForbidden when the wallet belongs to someone else.
type WalletService interface {
	CreateWallet(ctx context.Context, cmd CreateWalletCmd) (*domain.Wallet, error)
	GetWallet(ctx context.Context, userID, id string) (*domain.Wallet, error)
	// GetWalletByOwner returns the user's wallet in the given currency.
	GetWalletByOwner(ctx context.Context, userID, currency string) (*domain.Wallet, error)
	Deposit(ctx context.Context, cmd MoneyCmd) (*domain.Transaction, error)
	Withdraw(ctx context.Context, cmd MoneyCmd) (*domain.Transaction, error)
	Transfer(ctx context.Context, cmd TransferCmd) (*domain.Transaction, error)
	ListEntries(ctx context.Context, userID, walletID string, q EntryQuery) ([]domain.HistoryEntry, error)

	// SubscribeBalance streams realtime balance changes for one wallet.
	// The channel is closed when ctx is cancelled.
	SubscribeBalance(ctx context.Context, userID, walletID string) (<-chan domain.BalanceChanged, error)
}

type CreateWalletCmd struct {
	OwnerID  string
	Currency string
}

type MoneyCmd struct {
	UserID         string
	WalletID       string
	Amount         int64
	IdempotencyKey string
}

type TransferCmd struct {
	UserID         string // must own FromWalletID
	FromWalletID   string
	ToWalletID     string
	Amount         int64
	Note           string
	IdempotencyKey string
}

type EntryQuery struct {
	Limit     int
	Offset    int
	Direction domain.EntryDirection
}
