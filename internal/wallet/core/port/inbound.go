package port

import (
	"context"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
)

// WalletService is the inbound (driving) port: the use cases exposed to
// adapters such as the HTTP handler.
type WalletService interface {
	CreateWallet(ctx context.Context, cmd CreateWalletCmd) (*domain.Wallet, error)
	GetWallet(ctx context.Context, id string) (*domain.Wallet, error)
	Deposit(ctx context.Context, cmd MoneyCmd) (*domain.Transaction, error)
	Withdraw(ctx context.Context, cmd MoneyCmd) (*domain.Transaction, error)
	Transfer(ctx context.Context, cmd TransferCmd) (*domain.Transaction, error)
	ListEntries(ctx context.Context, walletID string, limit, offset int) ([]domain.Entry, error)

	// SubscribeBalance streams realtime balance changes for one wallet.
	// The channel is closed when ctx is cancelled.
	SubscribeBalance(ctx context.Context, walletID string) (<-chan domain.BalanceChanged, error)
}

type CreateWalletCmd struct {
	OwnerID  string
	Currency string
}

type MoneyCmd struct {
	WalletID       string
	Amount         int64
	IdempotencyKey string
}

type TransferCmd struct {
	FromWalletID   string
	ToWalletID     string
	Amount         int64
	IdempotencyKey string
}
