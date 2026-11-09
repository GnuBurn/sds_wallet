package domain

import "errors"

// Domain errors. Adapters translate these into transport-specific responses
// (e.g. HTTP status codes); the core never knows about HTTP.
var (
	ErrWalletNotFound          = errors.New("wallet not found")
	ErrWalletExists            = errors.New("wallet already exists for this owner and currency")
	ErrWalletInactive          = errors.New("wallet is not active")
	ErrTransactionNotFound     = errors.New("transaction not found")
	ErrInsufficientFunds       = errors.New("insufficient funds")
	ErrBalanceOverflow         = errors.New("balance overflow")
	ErrInvalidAmount           = errors.New("amount must be greater than zero")
	ErrInvalidCurrency         = errors.New("currency must be a 3-letter ISO 4217 code")
	ErrInvalidOwner            = errors.New("owner id is required")
	ErrCurrencyMismatch        = errors.New("wallets have different currencies")
	ErrSameWallet              = errors.New("cannot transfer to the same wallet")
	ErrMissingIdempotencyKey   = errors.New("idempotency key is required")
	ErrIdempotencyKeyReused    = errors.New("idempotency key was already used for a different request")
	ErrDuplicateIdempotencyKey = errors.New("duplicate idempotency key")
)
