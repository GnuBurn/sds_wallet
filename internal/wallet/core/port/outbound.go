package port

import (
	"context"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
)

// Outbound (driven) ports: what the core needs from infrastructure.

type WalletRepository interface {
	Create(ctx context.Context, w *domain.Wallet) error
	Get(ctx context.Context, id string) (*domain.Wallet, error)
	GetByOwner(ctx context.Context, ownerID string, currency domain.Currency) (*domain.Wallet, error)
	// GetForUpdate loads the wallet and locks it until the unit of work ends.
	GetForUpdate(ctx context.Context, id string) (*domain.Wallet, error)
	Update(ctx context.Context, w *domain.Wallet) error
}

type TransactionRepository interface {
	// Create stores the transaction and its entries. Returns
	// domain.ErrDuplicateIdempotencyKey if the key already exists.
	Create(ctx context.Context, tx *domain.Transaction) error
	FindByIdempotencyKey(ctx context.Context, key string) (*domain.Transaction, error)
	// ListEntries returns a wallet's history, newest first.
	ListEntries(ctx context.Context, walletID string, q EntryQuery) ([]domain.HistoryEntry, error)
}

// EventPublisher publishes domain events. Implementations used inside a
// UnitOfWork must only deliver events if the unit of work commits.
type EventPublisher interface {
	PublishBalanceChanged(ctx context.Context, e domain.BalanceChanged) error
}

// EventSubscriber delivers balance events for one wallet until ctx ends.
type EventSubscriber interface {
	SubscribeBalance(ctx context.Context, walletID string) (<-chan domain.BalanceChanged, error)
}

// Repositories are the transaction-scoped dependencies handed to a unit of work.
type Repositories struct {
	Wallets      WalletRepository
	Transactions TransactionRepository
	Events       EventPublisher
}

// UnitOfWork runs fn atomically: everything done through the given
// Repositories is committed together or rolled back together.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, r Repositories) error) error
}
