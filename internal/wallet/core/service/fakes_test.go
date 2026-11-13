package service

import (
	"context"
	"sync"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

// memStore is an in-memory implementation of all outbound ports.
// The unit of work works on a copy and only swaps it in on success,
// which gives the same all-or-nothing behaviour as a DB transaction.
type memStore struct {
	mu      sync.Mutex
	wallets map[string]domain.Wallet
	txs     map[string]domain.Transaction // by idempotency key
	events  []domain.BalanceChanged
}

func newMemStore() *memStore {
	return &memStore{wallets: map[string]domain.Wallet{}, txs: map[string]domain.Transaction{}}
}

func (m *memStore) clone() *memStore {
	c := newMemStore()
	for k, v := range m.wallets {
		c.wallets[k] = v
	}
	for k, v := range m.txs {
		c.txs[k] = v
	}
	return c
}

// --- UnitOfWork ---

func (m *memStore) Do(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.clone()
	if err := fn(ctx, port.Repositories{Wallets: work, Transactions: work.asTxRepo(), Events: work}); err != nil {
		return err
	}
	m.wallets, m.txs = work.wallets, work.txs
	m.events = append(m.events, work.events...)
	return nil
}

// --- WalletRepository ---

func (m *memStore) Create(_ context.Context, w *domain.Wallet) error {
	for _, existing := range m.wallets {
		if existing.OwnerID == w.OwnerID && existing.Currency == w.Currency {
			return domain.ErrWalletExists
		}
	}
	m.wallets[w.ID] = *w
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*domain.Wallet, error) {
	w, ok := m.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	return &w, nil
}

func (m *memStore) GetByOwner(_ context.Context, ownerID string, currency domain.Currency) (*domain.Wallet, error) {
	for _, w := range m.wallets {
		if w.OwnerID == ownerID && w.Currency == currency {
			return &w, nil
		}
	}
	return nil, domain.ErrWalletNotFound
}

func (m *memStore) GetForUpdate(ctx context.Context, id string) (*domain.Wallet, error) {
	return m.Get(ctx, id)
}

func (m *memStore) Update(_ context.Context, w *domain.Wallet) error {
	w.Version++
	m.wallets[w.ID] = *w
	return nil
}

// --- TransactionRepository (methods prefixed to avoid clashing names) ---

type memTxRepo struct{ *memStore }

func (m *memStore) asTxRepo() port.TransactionRepository { return memTxRepo{m} }

func (r memTxRepo) Create(_ context.Context, tx *domain.Transaction) error {
	if _, ok := r.txs[tx.IdempotencyKey]; ok {
		return domain.ErrDuplicateIdempotencyKey
	}
	r.txs[tx.IdempotencyKey] = *tx
	return nil
}

func (r memTxRepo) FindByIdempotencyKey(_ context.Context, key string) (*domain.Transaction, error) {
	tx, ok := r.txs[key]
	if !ok {
		return nil, domain.ErrTransactionNotFound
	}
	return &tx, nil
}

func (r memTxRepo) ListEntries(_ context.Context, walletID string, q port.EntryQuery) ([]domain.HistoryEntry, error) {
	var out []domain.HistoryEntry
	for _, tx := range r.txs {
		for _, e := range tx.Entries {
			if e.WalletID != walletID ||
				(q.Direction == domain.DirectionIn && e.Amount < 0) ||
				(q.Direction == domain.DirectionOut && e.Amount > 0) {
				continue
			}
			h := domain.HistoryEntry{Entry: e, Type: tx.Type, Note: tx.Note}
			if tx.Type == domain.TxTransfer {
				h.CounterpartyWalletID = tx.FromWalletID
				if tx.FromWalletID == walletID {
					h.CounterpartyWalletID = tx.ToWalletID
				}
				h.CounterpartyOwnerID = r.wallets[h.CounterpartyWalletID].OwnerID
			}
			out = append(out, h)
		}
	}
	return out, nil
}

// --- EventPublisher / EventSubscriber ---

func (m *memStore) PublishBalanceChanged(_ context.Context, e domain.BalanceChanged) error {
	m.events = append(m.events, e)
	return nil
}

func (m *memStore) SubscribeBalance(ctx context.Context, _ string) (<-chan domain.BalanceChanged, error) {
	ch := make(chan domain.BalanceChanged)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}
