package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

func newTestService(t *testing.T) (*WalletService, *memStore) {
	t.Helper()
	store := newMemStore()
	n := 0
	svc := New(Deps{
		UnitOfWork:   store,
		Wallets:      store,
		Transactions: store.asTxRepo(),
		Subscriber:   store,
		NewID:        func() string { n++; return fmt.Sprintf("id-%03d", n) },
	})
	return svc, store
}

func mustWallet(t *testing.T, svc *WalletService, owner, cur string) *domain.Wallet {
	t.Helper()
	w, err := svc.CreateWallet(context.Background(), port.CreateWalletCmd{OwnerID: owner, Currency: cur})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func balance(t *testing.T, svc *WalletService, id string) int64 {
	t.Helper()
	w, err := svc.GetWallet(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return w.Balance
}

func TestDepositWithdraw(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	w := mustWallet(t, svc, "alice", "THB")

	if _, err := svc.Deposit(ctx, port.MoneyCmd{WalletID: w.ID, Amount: 1000, IdempotencyKey: "k1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Withdraw(ctx, port.MoneyCmd{WalletID: w.ID, Amount: 300, IdempotencyKey: "k2"}); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, svc, w.ID); got != 700 {
		t.Fatalf("balance = %d, want 700", got)
	}

	_, err := svc.Withdraw(ctx, port.MoneyCmd{WalletID: w.ID, Amount: 701, IdempotencyKey: "k3"})
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}

	if len(store.events) != 2 {
		t.Fatalf("published %d events, want 2 (failed ops must not publish)", len(store.events))
	}
	if last := store.events[1]; last.Balance != 700 || last.Amount != -300 {
		t.Fatalf("last event = %+v", last)
	}
}

func TestIdempotency(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	w := mustWallet(t, svc, "alice", "THB")

	cmd := port.MoneyCmd{WalletID: w.ID, Amount: 500, IdempotencyKey: "same-key"}
	first, err := svc.Deposit(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Deposit(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("replay returned a new transaction")
	}
	if got := balance(t, svc, w.ID); got != 500 {
		t.Fatalf("balance = %d, want 500 (deposit applied twice)", got)
	}

	cmd.Amount = 999
	if _, err := svc.Deposit(ctx, cmd); !errors.Is(err, domain.ErrIdempotencyKeyReused) {
		t.Fatalf("err = %v, want ErrIdempotencyKeyReused", err)
	}
	if _, err := svc.Deposit(ctx, port.MoneyCmd{WalletID: w.ID, Amount: 1}); !errors.Is(err, domain.ErrMissingIdempotencyKey) {
		t.Fatalf("err = %v, want ErrMissingIdempotencyKey", err)
	}
}

func TestTransfer(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	alice := mustWallet(t, svc, "alice", "THB")
	bob := mustWallet(t, svc, "bob", "THB")
	usd := mustWallet(t, svc, "carol", "USD")

	if _, err := svc.Deposit(ctx, port.MoneyCmd{WalletID: alice.ID, Amount: 1000, IdempotencyKey: "d1"}); err != nil {
		t.Fatal(err)
	}

	tx, err := svc.Transfer(ctx, port.TransferCmd{FromWalletID: alice.ID, ToWalletID: bob.ID, Amount: 250, IdempotencyKey: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(tx.Entries))
	}
	if a, b := balance(t, svc, alice.ID), balance(t, svc, bob.ID); a != 750 || b != 250 {
		t.Fatalf("balances alice=%d bob=%d, want 750/250", a, b)
	}
	if len(store.events) != 3 {
		t.Fatalf("events = %d, want 3", len(store.events))
	}

	failures := []struct {
		name string
		cmd  port.TransferCmd
		want error
	}{
		{"insufficient", port.TransferCmd{FromWalletID: bob.ID, ToWalletID: alice.ID, Amount: 251, IdempotencyKey: "t2"}, domain.ErrInsufficientFunds},
		{"currency mismatch", port.TransferCmd{FromWalletID: alice.ID, ToWalletID: usd.ID, Amount: 1, IdempotencyKey: "t3"}, domain.ErrCurrencyMismatch},
		{"same wallet", port.TransferCmd{FromWalletID: alice.ID, ToWalletID: alice.ID, Amount: 1, IdempotencyKey: "t4"}, domain.ErrSameWallet},
		{"unknown wallet", port.TransferCmd{FromWalletID: alice.ID, ToWalletID: "nope", Amount: 1, IdempotencyKey: "t5"}, domain.ErrWalletNotFound},
	}
	for _, f := range failures {
		t.Run(f.name, func(t *testing.T) {
			if _, err := svc.Transfer(ctx, f.cmd); !errors.Is(err, f.want) {
				t.Fatalf("err = %v, want %v", err, f.want)
			}
		})
	}
	if a, b := balance(t, svc, alice.ID), balance(t, svc, bob.ID); a != 750 || b != 250 {
		t.Fatalf("failed transfers changed balances: alice=%d bob=%d", a, b)
	}
}

func TestCreateWalletValidation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	mustWallet(t, svc, "alice", "THB")

	cases := []struct {
		cmd  port.CreateWalletCmd
		want error
	}{
		{port.CreateWalletCmd{OwnerID: "alice", Currency: "thb"}, domain.ErrWalletExists},
		{port.CreateWalletCmd{OwnerID: "", Currency: "THB"}, domain.ErrInvalidOwner},
		{port.CreateWalletCmd{OwnerID: "bob", Currency: "BAHT"}, domain.ErrInvalidCurrency},
	}
	for _, c := range cases {
		if _, err := svc.CreateWallet(ctx, c.cmd); !errors.Is(err, c.want) {
			t.Errorf("CreateWallet(%+v) err = %v, want %v", c.cmd, err, c.want)
		}
	}
}
