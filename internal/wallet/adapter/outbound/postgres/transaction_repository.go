package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

type TransactionRepository struct {
	q querier
}

var _ port.TransactionRepository = (*TransactionRepository)(nil)

func NewTransactionRepository(q querier) *TransactionRepository {
	return &TransactionRepository{q: q}
}

func (r *TransactionRepository) Create(ctx context.Context, tx *domain.Transaction) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO transactions
		   (id, idempotency_key, type, amount, currency, from_wallet_id, to_wallet_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tx.ID, tx.IdempotencyKey, string(tx.Type), tx.Amount, string(tx.Currency),
		nullable(tx.FromWalletID), nullable(tx.ToWalletID), tx.CreatedAt,
	)
	if code, constraint := pgCode(err); code == codeUniqueViolation && constraint == "transactions_idempotency_key_key" {
		return domain.ErrDuplicateIdempotencyKey
	}
	if err != nil {
		return err
	}

	for i := range tx.Entries {
		e := &tx.Entries[i]
		if err := r.q.QueryRow(ctx,
			`INSERT INTO ledger_entries (transaction_id, wallet_id, amount, balance_after, created_at)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			e.TransactionID, e.WalletID, e.Amount, e.BalanceAfter, e.CreatedAt,
		).Scan(&e.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *TransactionRepository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Transaction, error) {
	var (
		tx           domain.Transaction
		typ, cur     string
		fromID, toID *string
	)
	err := r.q.QueryRow(ctx,
		`SELECT id, idempotency_key, type, amount, currency, from_wallet_id, to_wallet_id, created_at
		   FROM transactions WHERE idempotency_key = $1`, key,
	).Scan(&tx.ID, &tx.IdempotencyKey, &typ, &tx.Amount, &cur, &fromID, &toID, &tx.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrTransactionNotFound
	}
	if err != nil {
		return nil, err
	}
	tx.Type = domain.TransactionType(typ)
	tx.Currency = domain.Currency(cur)
	tx.FromWalletID = deref(fromID)
	tx.ToWalletID = deref(toID)

	tx.Entries, err = r.queryEntries(ctx,
		`SELECT id, transaction_id, wallet_id, amount, balance_after, created_at
		   FROM ledger_entries WHERE transaction_id = $1 ORDER BY id`, tx.ID)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (r *TransactionRepository) ListEntries(ctx context.Context, walletID string, limit, offset int) ([]domain.Entry, error) {
	return r.queryEntries(ctx,
		`SELECT id, transaction_id, wallet_id, amount, balance_after, created_at
		   FROM ledger_entries WHERE wallet_id = $1
		  ORDER BY id DESC LIMIT $2 OFFSET $3`, walletID, limit, offset)
}

func (r *TransactionRepository) queryEntries(ctx context.Context, sql string, args ...any) ([]domain.Entry, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Entry, error) {
		var e domain.Entry
		err := row.Scan(&e.ID, &e.TransactionID, &e.WalletID, &e.Amount, &e.BalanceAfter, &e.CreatedAt)
		return e, err
	})
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []domain.Entry{}
	}
	return entries, nil
}
