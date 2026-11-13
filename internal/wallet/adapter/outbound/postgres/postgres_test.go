package postgres_test

// Integration tests against a real Postgres. They are skipped unless
// TEST_DATABASE_URL is set, e.g.
//
//	make db
//	TEST_DATABASE_URL="postgres://wallet:wallet@localhost:5433/wallet?sslmode=disable" go test ./...
//
// Each test runs in its own freshly migrated schema, which is dropped afterwards.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GnuBurn/sds_wallet/internal/wallet/adapter/outbound/postgres"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/service"
)

var schemaSeq atomic.Int64

func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), schemaSeq.Add(1))
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

func newService(pool *pgxpool.Pool, sub port.EventSubscriber) *service.WalletService {
	return service.New(service.Deps{
		UnitOfWork:   postgres.NewUnitOfWork(pool),
		Wallets:      postgres.NewWalletRepository(pool),
		Transactions: postgres.NewTransactionRepository(pool),
		Subscriber:   sub,
		NewID:        newID,
	})
}

func mustWallet(t *testing.T, svc *service.WalletService, owner, cur string) *domain.Wallet {
	t.Helper()
	w, err := svc.CreateWallet(context.Background(), port.CreateWalletCmd{OwnerID: owner, Currency: cur})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func mustDeposit(t *testing.T, svc *service.WalletService, w *domain.Wallet, amount int64) {
	t.Helper()
	if _, err := svc.Deposit(context.Background(), port.MoneyCmd{
		UserID: w.OwnerID, WalletID: w.ID, Amount: amount, IdempotencyKey: newID(),
	}); err != nil {
		t.Fatal(err)
	}
}

func balanceOf(t *testing.T, svc *service.WalletService, w *domain.Wallet) int64 {
	t.Helper()
	got, err := svc.GetWallet(context.Background(), w.OwnerID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.Balance
}

func TestMigrateIsRepeatable(t *testing.T) {
	pool := newTestDB(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestWalletRepository(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewWalletRepository(newTestDB(t))
	now := time.Now().UTC().Truncate(time.Microsecond)

	w, err := domain.NewWallet(newID(), "u_1", "THB", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, w); err != nil {
		t.Fatal(err)
	}

	dup, _ := domain.NewWallet(newID(), "u_1", "THB", now)
	if err := repo.Create(ctx, dup); !errors.Is(err, domain.ErrWalletExists) {
		t.Fatalf("duplicate owner/currency: err = %v, want ErrWalletExists", err)
	}

	got, err := repo.Get(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != "u_1" || got.Currency != "THB" || got.Status != domain.WalletActive || !got.CreatedAt.Equal(now) {
		t.Fatalf("Get = %+v", got)
	}

	byOwner, err := repo.GetByOwner(ctx, "u_1", "THB")
	if err != nil || byOwner.ID != w.ID {
		t.Fatalf("GetByOwner = %+v, %v", byOwner, err)
	}
	if _, err := repo.GetByOwner(ctx, "u_1", "USD"); !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("GetByOwner other currency: err = %v, want ErrWalletNotFound", err)
	}
	if _, err := repo.Get(ctx, newID()); !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrWalletNotFound", err)
	}
	if _, err := repo.Get(ctx, "not-a-uuid"); !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("malformed id: err = %v, want ErrWalletNotFound", err)
	}

	// Optimistic concurrency: a stale version must not overwrite.
	stale := *got
	got.Balance = 100
	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Version)
	}
	stale.Balance = 999
	if err := repo.Update(ctx, &stale); err == nil {
		t.Fatal("update with stale version succeeded")
	}
}

func TestMoneyFlowAndHistory(t *testing.T) {
	ctx := context.Background()
	svc := newService(newTestDB(t), nil)
	alice := mustWallet(t, svc, "alice", "THB")
	bob := mustWallet(t, svc, "bob", "THB")

	mustDeposit(t, svc, alice, 1000)

	transfer := port.TransferCmd{
		UserID: "alice", FromWalletID: alice.ID, ToWalletID: bob.ID,
		Amount: 300, Note: "Dinner split", IdempotencyKey: newID(),
	}
	tx, err := svc.Transfer(ctx, transfer)
	if err != nil {
		t.Fatal(err)
	}

	// Replaying the same request returns the stored transaction, note included.
	again, err := svc.Transfer(ctx, transfer)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if again.ID != tx.ID || again.Note != "Dinner split" || len(again.Entries) != 2 {
		t.Fatalf("replay = %+v, want original transaction", again)
	}
	transfer.Note = "changed"
	if _, err := svc.Transfer(ctx, transfer); !errors.Is(err, domain.ErrIdempotencyKeyReused) {
		t.Fatalf("reused key, new note: err = %v, want ErrIdempotencyKeyReused", err)
	}

	if _, err := svc.Withdraw(ctx, port.MoneyCmd{UserID: "alice", WalletID: alice.ID, Amount: 100, IdempotencyKey: newID()}); err != nil {
		t.Fatal(err)
	}
	if a, b := balanceOf(t, svc, alice), balanceOf(t, svc, bob); a != 600 || b != 300 {
		t.Fatalf("balances alice=%d bob=%d, want 600/300", a, b)
	}

	hist, err := svc.ListEntries(ctx, "alice", alice.ID, port.EntryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("alice history = %d entries, want 3", len(hist))
	}
	// Newest first: withdrawal, transfer, deposit.
	wd, tr, dep := hist[0], hist[1], hist[2]
	if wd.Type != domain.TxWithdrawal || wd.Amount != -100 || wd.BalanceAfter != 600 || wd.CounterpartyWalletID != "" {
		t.Errorf("withdrawal entry = %+v", wd)
	}
	if tr.Type != domain.TxTransfer || tr.Amount != -300 || tr.Note != "Dinner split" ||
		tr.CounterpartyWalletID != bob.ID || tr.CounterpartyOwnerID != "bob" || tr.TransactionID != tx.ID {
		t.Errorf("transfer entry = %+v", tr)
	}
	if dep.Type != domain.TxDeposit || dep.Amount != 1000 || dep.CounterpartyOwnerID != "" {
		t.Errorf("deposit entry = %+v", dep)
	}

	bobHist, err := svc.ListEntries(ctx, "bob", bob.ID, port.EntryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(bobHist) != 1 || bobHist[0].Amount != 300 || bobHist[0].CounterpartyOwnerID != "alice" || bobHist[0].Note != "Dinner split" {
		t.Fatalf("bob history = %+v", bobHist)
	}

	in, err := svc.ListEntries(ctx, "alice", alice.ID, port.EntryQuery{Direction: domain.DirectionIn})
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 || in[0].Type != domain.TxDeposit {
		t.Fatalf("direction=in = %+v, want only the deposit", in)
	}
	out, err := svc.ListEntries(ctx, "alice", alice.ID, port.EntryQuery{Direction: domain.DirectionOut})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("direction=out = %d entries, want 2", len(out))
	}

	page, err := svc.ListEntries(ctx, "alice", alice.ID, port.EntryQuery{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != tr.ID {
		t.Fatalf("limit=1 offset=1 = %+v, want the transfer entry", page)
	}

	empty, err := svc.ListEntries(ctx, "bob", bob.ID, port.EntryQuery{Direction: domain.DirectionOut})
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("no matches should be an empty, non-nil slice; got %#v", empty)
	}
}

func TestFailedTransferRollsBack(t *testing.T) {
	ctx := context.Background()
	svc := newService(newTestDB(t), nil)
	alice := mustWallet(t, svc, "alice", "THB")
	bob := mustWallet(t, svc, "bob", "THB")
	usd := mustWallet(t, svc, "carol", "USD")
	mustDeposit(t, svc, alice, 500)

	cases := []struct {
		to     string
		amount int64
		want   error
	}{
		{bob.ID, 501, domain.ErrInsufficientFunds},
		{usd.ID, 1, domain.ErrCurrencyMismatch},
		{newID(), 1, domain.ErrWalletNotFound},
	}
	for _, c := range cases {
		_, err := svc.Transfer(ctx, port.TransferCmd{UserID: "alice", FromWalletID: alice.ID, ToWalletID: c.to, Amount: c.amount, IdempotencyKey: newID()})
		if !errors.Is(err, c.want) {
			t.Errorf("err = %v, want %v", err, c.want)
		}
	}
	if a, b := balanceOf(t, svc, alice), balanceOf(t, svc, bob); a != 500 || b != 0 {
		t.Fatalf("balances alice=%d bob=%d, want 500/0", a, b)
	}
	hist, err := svc.ListEntries(ctx, "alice", alice.ID, port.EntryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Fatalf("failed transfers left %d ledger entries, want only the deposit", len(hist)-1)
	}
}

// Many transfers in both directions at once must neither deadlock nor
// create or lose money.
func TestConcurrentTransfers(t *testing.T) {
	ctx := context.Background()
	svc := newService(newTestDB(t), nil)
	alice := mustWallet(t, svc, "alice", "THB")
	bob := mustWallet(t, svc, "bob", "THB")
	mustDeposit(t, svc, alice, 1000)
	mustDeposit(t, svc, bob, 1000)

	const workers = 40
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		from, to := alice, bob
		if i%2 == 1 {
			from, to = bob, alice
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Transfer(ctx, port.TransferCmd{
				UserID: from.OwnerID, FromWalletID: from.ID, ToWalletID: to.ID, Amount: 70, IdempotencyKey: newID(),
			})
			if err != nil && !errors.Is(err, domain.ErrInsufficientFunds) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("transfer: %v", err)
	}

	a, b := balanceOf(t, svc, alice), balanceOf(t, svc, bob)
	if a+b != 2000 || a < 0 || b < 0 {
		t.Fatalf("balances alice=%d bob=%d: total must stay 2000", a, b)
	}
	// The last ledger entry of each wallet must match its balance.
	for _, w := range []*domain.Wallet{alice, bob} {
		hist, err := svc.ListEntries(ctx, w.OwnerID, w.ID, port.EntryQuery{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if hist[0].BalanceAfter != balanceOf(t, svc, w) {
			t.Errorf("%s: last balance_after = %d, wallet balance = %d", w.OwnerID, hist[0].BalanceAfter, balanceOf(t, svc, w))
		}
	}
}

// Concurrent requests with the same idempotency key move money once and all
// return the same transaction.
func TestConcurrentIdempotentDeposits(t *testing.T) {
	ctx := context.Background()
	svc := newService(newTestDB(t), nil)
	w := mustWallet(t, svc, "alice", "THB")

	const workers = 10
	key := newID()
	ids := make(chan string, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := svc.Deposit(ctx, port.MoneyCmd{UserID: "alice", WalletID: w.ID, Amount: 500, IdempotencyKey: key})
			if err != nil {
				t.Errorf("deposit: %v", err)
				return
			}
			ids <- tx.ID
		}()
	}
	wg.Wait()
	close(ids)

	var first string
	for id := range ids {
		if first == "" {
			first = id
		} else if id != first {
			t.Fatalf("got transactions %s and %s for one idempotency key", first, id)
		}
	}
	if got := balanceOf(t, svc, w); got != 500 {
		t.Fatalf("balance = %d, want 500 (deposit applied more than once)", got)
	}
}

// signalHandler is a slog.Handler that closes ready when the listener logs
// that it is connected, so tests know LISTEN is active.
type signalHandler struct {
	slog.Handler
	once  *sync.Once
	ready chan struct{}
}

func (h signalHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "balance listener connected" {
		h.once.Do(func() { close(h.ready) })
	}
	return nil
}

func (h signalHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h signalHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h signalHandler) WithGroup(string) slog.Handler            { return h }

func TestListenerDeliversOnlyCommittedEvents(t *testing.T) {
	pool := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	log := slog.New(signalHandler{once: &sync.Once{}, ready: ready})
	listener := postgres.NewListener(pool, log)
	go listener.Run(ctx)
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("listener did not connect")
	}

	svc := newService(pool, listener)
	w := mustWallet(t, svc, "alice", "THB")
	events, err := svc.SubscribeBalance(ctx, "alice", w.ID)
	if err != nil {
		t.Fatal(err)
	}

	// An event published in a rolled-back unit of work must not be delivered.
	uow := postgres.NewUnitOfWork(pool)
	boom := errors.New("boom")
	err = uow.Do(ctx, func(ctx context.Context, r port.Repositories) error {
		if err := r.Events.PublishBalanceChanged(ctx, domain.BalanceChanged{WalletID: w.ID, Balance: 999_999, Currency: "THB"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("uow err = %v", err)
	}

	tx, err := svc.Deposit(ctx, port.MoneyCmd{UserID: "alice", WalletID: w.ID, Amount: 250, IdempotencyKey: newID()})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case e := <-events:
		if e.Balance == 999_999 {
			t.Fatal("received an event from a rolled-back transaction")
		}
		if e.TransactionID != tx.ID || e.Amount != 250 || e.Balance != 250 || e.Type != domain.TxDeposit || e.Currency != "THB" {
			t.Fatalf("event = %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no balance event received")
	}

	// Events for other wallets are not delivered to this subscriber.
	other := mustWallet(t, svc, "bob", "THB")
	mustDeposit(t, svc, other, 1)
	select {
	case e := <-events:
		t.Fatalf("received event for another wallet: %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}
