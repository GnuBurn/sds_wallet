package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

type Deps struct {
	UnitOfWork   port.UnitOfWork
	Wallets      port.WalletRepository      // non-transactional reads
	Transactions port.TransactionRepository // non-transactional reads
	Subscriber   port.EventSubscriber
	NewID        func() string
	Now          func() time.Time
}

type WalletService struct {
	d Deps
}

var _ port.WalletService = (*WalletService)(nil)

func New(d Deps) *WalletService {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &WalletService{d: d}
}

func (s *WalletService) CreateWallet(ctx context.Context, cmd port.CreateWalletCmd) (*domain.Wallet, error) {
	cur, err := domain.ParseCurrency(cmd.Currency)
	if err != nil {
		return nil, err
	}
	w, err := domain.NewWallet(s.d.NewID(), cmd.OwnerID, cur, s.d.Now())
	if err != nil {
		return nil, err
	}
	if err := s.d.Wallets.Create(ctx, w); err != nil {
		return nil, err
	}
	return w, nil
}

func (s *WalletService) GetWallet(ctx context.Context, userID, id string) (*domain.Wallet, error) {
	return s.authorize(ctx, userID, id)
}

func (s *WalletService) GetWalletByOwner(ctx context.Context, userID, currency string) (*domain.Wallet, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, domain.ErrUnauthenticated
	}
	cur, err := domain.ParseCurrency(currency)
	if err != nil {
		return nil, err
	}
	return s.d.Wallets.GetByOwner(ctx, userID, cur)
}

func (s *WalletService) ListEntries(ctx context.Context, userID, walletID string, q port.EntryQuery) ([]domain.HistoryEntry, error) {
	if _, err := s.authorize(ctx, userID, walletID); err != nil {
		return nil, err
	}
	if q.Limit <= 0 {
		q.Limit = defaultPageSize
	}
	q.Limit = min(q.Limit, maxPageSize)
	q.Offset = max(q.Offset, 0)
	return s.d.Transactions.ListEntries(ctx, walletID, q)
}

func (s *WalletService) SubscribeBalance(ctx context.Context, userID, walletID string) (<-chan domain.BalanceChanged, error) {
	if _, err := s.authorize(ctx, userID, walletID); err != nil {
		return nil, err
	}
	return s.d.Subscriber.SubscribeBalance(ctx, walletID)
}

// authorize loads the wallet and checks that userID owns it. A wallet's
// owner never changes, so checking outside the unit of work is safe.
func (s *WalletService) authorize(ctx context.Context, userID, walletID string) (*domain.Wallet, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, domain.ErrUnauthenticated
	}
	w, err := s.d.Wallets.Get(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if w.OwnerID != userID {
		return nil, domain.ErrForbidden
	}
	return w, nil
}

func (s *WalletService) Deposit(ctx context.Context, cmd port.MoneyCmd) (*domain.Transaction, error) {
	if _, err := s.authorize(ctx, cmd.UserID, cmd.WalletID); err != nil {
		return nil, err
	}
	req := &domain.Transaction{
		IdempotencyKey: cmd.IdempotencyKey,
		Type:           domain.TxDeposit,
		Amount:         cmd.Amount,
		ToWalletID:     cmd.WalletID,
	}
	return s.execute(ctx, req, func(ctx context.Context, r port.Repositories, tx *domain.Transaction) error {
		w, err := r.Wallets.GetForUpdate(ctx, cmd.WalletID)
		if err != nil {
			return err
		}
		if err := w.Credit(tx.Amount, tx.CreatedAt); err != nil {
			return err
		}
		tx.Currency = w.Currency
		return s.apply(ctx, r, tx, w, tx.Amount)
	})
}

func (s *WalletService) Withdraw(ctx context.Context, cmd port.MoneyCmd) (*domain.Transaction, error) {
	if _, err := s.authorize(ctx, cmd.UserID, cmd.WalletID); err != nil {
		return nil, err
	}
	req := &domain.Transaction{
		IdempotencyKey: cmd.IdempotencyKey,
		Type:           domain.TxWithdrawal,
		Amount:         cmd.Amount,
		FromWalletID:   cmd.WalletID,
	}
	return s.execute(ctx, req, func(ctx context.Context, r port.Repositories, tx *domain.Transaction) error {
		w, err := r.Wallets.GetForUpdate(ctx, cmd.WalletID)
		if err != nil {
			return err
		}
		if err := w.Debit(tx.Amount, tx.CreatedAt); err != nil {
			return err
		}
		tx.Currency = w.Currency
		return s.apply(ctx, r, tx, w, -tx.Amount)
	})
}

func (s *WalletService) Transfer(ctx context.Context, cmd port.TransferCmd) (*domain.Transaction, error) {
	if cmd.FromWalletID == cmd.ToWalletID {
		return nil, domain.ErrSameWallet
	}
	note, err := domain.NormalizeNote(cmd.Note)
	if err != nil {
		return nil, err
	}
	if _, err := s.authorize(ctx, cmd.UserID, cmd.FromWalletID); err != nil {
		return nil, err
	}
	req := &domain.Transaction{
		IdempotencyKey: cmd.IdempotencyKey,
		Type:           domain.TxTransfer,
		Amount:         cmd.Amount,
		FromWalletID:   cmd.FromWalletID,
		ToWalletID:     cmd.ToWalletID,
		Note:           note,
	}
	return s.execute(ctx, req, func(ctx context.Context, r port.Repositories, tx *domain.Transaction) error {
		// Lock wallets in a fixed order so concurrent opposite transfers
		// (A->B and B->A) cannot deadlock.
		firstID, secondID := cmd.FromWalletID, cmd.ToWalletID
		if firstID > secondID {
			firstID, secondID = secondID, firstID
		}
		first, err := r.Wallets.GetForUpdate(ctx, firstID)
		if err != nil {
			return err
		}
		second, err := r.Wallets.GetForUpdate(ctx, secondID)
		if err != nil {
			return err
		}
		from, to := first, second
		if from.ID != cmd.FromWalletID {
			from, to = second, first
		}

		if from.Currency != to.Currency {
			return domain.ErrCurrencyMismatch
		}
		if err := from.Debit(tx.Amount, tx.CreatedAt); err != nil {
			return err
		}
		if err := to.Credit(tx.Amount, tx.CreatedAt); err != nil {
			return err
		}
		tx.Currency = from.Currency
		if err := s.apply(ctx, r, tx, from, -tx.Amount); err != nil {
			return err
		}
		return s.apply(ctx, r, tx, to, tx.Amount)
	})
}

// apply persists a wallet's new balance, adds its ledger entry to tx and
// publishes the realtime event (delivered only after commit).
func (s *WalletService) apply(ctx context.Context, r port.Repositories, tx *domain.Transaction, w *domain.Wallet, signedAmount int64) error {
	if err := r.Wallets.Update(ctx, w); err != nil {
		return err
	}
	tx.Entries = append(tx.Entries, domain.Entry{
		TransactionID: tx.ID,
		WalletID:      w.ID,
		Amount:        signedAmount,
		BalanceAfter:  w.Balance,
		CreatedAt:     tx.CreatedAt,
	})
	return r.Events.PublishBalanceChanged(ctx, domain.BalanceChanged{
		WalletID:      w.ID,
		TransactionID: tx.ID,
		Type:          tx.Type,
		Amount:        signedAmount,
		Balance:       w.Balance,
		Currency:      w.Currency,
		OccurredAt:    tx.CreatedAt,
	})
}

type mutation func(ctx context.Context, r port.Repositories, tx *domain.Transaction) error

// execute runs a money movement atomically and idempotently. Repeating a
// request with the same idempotency key returns the original transaction
// instead of moving money twice.
func (s *WalletService) execute(ctx context.Context, req *domain.Transaction, mutate mutation) (*domain.Transaction, error) {
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		return nil, domain.ErrMissingIdempotencyKey
	}
	if req.Amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	var result *domain.Transaction
	err := s.d.UnitOfWork.Do(ctx, func(ctx context.Context, r port.Repositories) error {
		existing, err := r.Transactions.FindByIdempotencyKey(ctx, req.IdempotencyKey)
		switch {
		case err == nil:
			result, err = replay(existing, req)
			return err
		case !errors.Is(err, domain.ErrTransactionNotFound):
			return err
		}

		tx := *req
		tx.ID = s.d.NewID()
		tx.CreatedAt = s.d.Now()
		tx.Entries = nil
		if err := mutate(ctx, r, &tx); err != nil {
			return err
		}
		if err := r.Transactions.Create(ctx, &tx); err != nil {
			return err
		}
		result = &tx
		return nil
	})

	// A concurrent request with the same key won the race; return its result.
	if errors.Is(err, domain.ErrDuplicateIdempotencyKey) {
		existing, ferr := s.d.Transactions.FindByIdempotencyKey(ctx, req.IdempotencyKey)
		if ferr != nil {
			return nil, ferr
		}
		return replay(existing, req)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func replay(existing, req *domain.Transaction) (*domain.Transaction, error) {
	if !existing.SameRequest(req) {
		return nil, domain.ErrIdempotencyKeyReused
	}
	return existing, nil
}
