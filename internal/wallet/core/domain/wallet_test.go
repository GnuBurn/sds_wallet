package domain

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestWalletCreditDebit(t *testing.T) {
	now := time.Now()
	w, err := NewWallet("w1", "owner", "THB", now)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Credit(1000, now); err != nil {
		t.Fatal(err)
	}
	if err := w.Debit(400, now); err != nil {
		t.Fatal(err)
	}
	if w.Balance != 600 {
		t.Fatalf("balance = %d, want 600", w.Balance)
	}

	tests := []struct {
		name string
		op   func() error
		want error
	}{
		{"debit more than balance", func() error { return w.Debit(601, now) }, ErrInsufficientFunds},
		{"zero credit", func() error { return w.Credit(0, now) }, ErrInvalidAmount},
		{"negative debit", func() error { return w.Debit(-1, now) }, ErrInvalidAmount},
		{"overflow", func() error { return w.Credit(math.MaxInt64, now) }, ErrBalanceOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.op(); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if w.Balance != 600 {
				t.Fatalf("balance changed to %d on failed op", w.Balance)
			}
		})
	}
}

func TestFrozenWalletRejectsMovements(t *testing.T) {
	w, _ := NewWallet("w1", "owner", "THB", time.Now())
	w.Status = WalletFrozen
	if err := w.Credit(1, time.Now()); !errors.Is(err, ErrWalletInactive) {
		t.Fatalf("err = %v, want ErrWalletInactive", err)
	}
}

func TestParseCurrency(t *testing.T) {
	if c, err := ParseCurrency(" thb "); err != nil || c != "THB" {
		t.Fatalf("got %q, %v", c, err)
	}
	for _, bad := range []string{"", "TH", "THBB", "T1B"} {
		if _, err := ParseCurrency(bad); !errors.Is(err, ErrInvalidCurrency) {
			t.Errorf("ParseCurrency(%q) err = %v", bad, err)
		}
	}
}
