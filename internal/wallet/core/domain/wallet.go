package domain

import (
	"math"
	"strings"
	"time"
)

type WalletStatus string

const (
	WalletActive WalletStatus = "active"
	WalletFrozen WalletStatus = "frozen"
)

type Wallet struct {
	ID        string
	OwnerID   string
	Currency  Currency
	Balance   int64 // minor units
	Status    WalletStatus
	Version   int64 // incremented on every balance change (optimistic concurrency)
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewWallet(id, ownerID string, currency Currency, now time.Time) (*Wallet, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return nil, ErrInvalidOwner
	}
	return &Wallet{
		ID:        id,
		OwnerID:   ownerID,
		Currency:  currency,
		Status:    WalletActive,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (w *Wallet) Credit(amount int64, now time.Time) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if w.Status != WalletActive {
		return ErrWalletInactive
	}
	if w.Balance > math.MaxInt64-amount {
		return ErrBalanceOverflow
	}
	w.Balance += amount
	w.UpdatedAt = now
	return nil
}

func (w *Wallet) Debit(amount int64, now time.Time) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if w.Status != WalletActive {
		return ErrWalletInactive
	}
	if w.Balance < amount {
		return ErrInsufficientFunds
	}
	w.Balance -= amount
	w.UpdatedAt = now
	return nil
}
