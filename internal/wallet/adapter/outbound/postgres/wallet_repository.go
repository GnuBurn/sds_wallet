package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

type WalletRepository struct {
	q querier
}

var _ port.WalletRepository = (*WalletRepository)(nil)

func NewWalletRepository(q querier) *WalletRepository {
	return &WalletRepository{q: q}
}

const walletColumns = `id, owner_id, currency, balance, status, version, created_at, updated_at`

func (r *WalletRepository) Create(ctx context.Context, w *domain.Wallet) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO wallets (`+walletColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		w.ID, w.OwnerID, string(w.Currency), w.Balance, string(w.Status), w.Version, w.CreatedAt, w.UpdatedAt,
	)
	if code, constraint := pgCode(err); code == codeUniqueViolation && constraint == "wallets_owner_currency_key" {
		return domain.ErrWalletExists
	}
	return err
}

func (r *WalletRepository) Get(ctx context.Context, id string) (*domain.Wallet, error) {
	return r.get(ctx, `SELECT `+walletColumns+` FROM wallets WHERE id = $1`, id)
}

func (r *WalletRepository) GetForUpdate(ctx context.Context, id string) (*domain.Wallet, error) {
	return r.get(ctx, `SELECT `+walletColumns+` FROM wallets WHERE id = $1 FOR UPDATE`, id)
}

func (r *WalletRepository) get(ctx context.Context, sql, id string) (*domain.Wallet, error) {
	var (
		w        domain.Wallet
		currency string
		status   string
	)
	err := r.q.QueryRow(ctx, sql, id).Scan(
		&w.ID, &w.OwnerID, &currency, &w.Balance, &status, &w.Version, &w.CreatedAt, &w.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrWalletNotFound
	}
	if code, _ := pgCode(err); code == codeInvalidText {
		return nil, domain.ErrWalletNotFound // not a valid UUID, so it can't exist
	}
	if err != nil {
		return nil, err
	}
	w.Currency = domain.Currency(currency)
	w.Status = domain.WalletStatus(status)
	return &w, nil
}

// Update saves balance/status using optimistic concurrency on version.
func (r *WalletRepository) Update(ctx context.Context, w *domain.Wallet) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE wallets
		    SET balance = $1, status = $2, version = version + 1, updated_at = $3
		  WHERE id = $4 AND version = $5`,
		w.Balance, string(w.Status), w.UpdatedAt, w.ID, w.Version,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("wallet %s was modified concurrently", w.ID)
	}
	w.Version++
	return nil
}
