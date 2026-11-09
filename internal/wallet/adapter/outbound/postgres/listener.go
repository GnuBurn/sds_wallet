package postgres

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

const subscriberBuffer = 32

// Listener holds one LISTEN connection per service instance and fans
// notifications out to in-process subscribers by wallet ID.
type Listener struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	mu   sync.Mutex
	subs map[string]map[chan domain.BalanceChanged]struct{}
}

var _ port.EventSubscriber = (*Listener)(nil)

func NewListener(pool *pgxpool.Pool, log *slog.Logger) *Listener {
	return &Listener{
		pool: pool,
		log:  log,
		subs: make(map[string]map[chan domain.BalanceChanged]struct{}),
	}
}

// Run listens until ctx is cancelled, reconnecting with backoff on errors.
func (l *Listener) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := l.listen(ctx)
		if ctx.Err() != nil {
			return
		}
		l.log.Error("balance listener disconnected, retrying", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (l *Listener) listen(ctx context.Context) error {
	pooled, err := l.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// Take the connection out of the pool: it stays in LISTEN state, so it
	// must never be handed to other queries.
	conn := pooled.Hijack()
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{BalanceChannel}.Sanitize()); err != nil {
		return err
	}
	l.log.Info("balance listener connected", "channel", BalanceChannel)

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var msg balanceChangedMsg
		if err := json.Unmarshal([]byte(n.Payload), &msg); err != nil {
			l.log.Warn("bad balance notification", "err", err)
			continue
		}
		l.dispatch(domain.BalanceChanged{
			WalletID:      msg.WalletID,
			TransactionID: msg.TransactionID,
			Type:          domain.TransactionType(msg.Type),
			Amount:        msg.Amount,
			Balance:       msg.Balance,
			Currency:      domain.Currency(msg.Currency),
			OccurredAt:    msg.OccurredAt,
		})
	}
}

func (l *Listener) dispatch(e domain.BalanceChanged) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ch := range l.subs[e.WalletID] {
		select {
		case ch <- e:
		default:
			// Slow consumer: drop rather than block everyone. Each event
			// carries the full balance, so the next one catches them up.
			l.log.Warn("dropping balance event for slow subscriber", "wallet_id", e.WalletID)
		}
	}
}

func (l *Listener) SubscribeBalance(ctx context.Context, walletID string) (<-chan domain.BalanceChanged, error) {
	ch := make(chan domain.BalanceChanged, subscriberBuffer)

	l.mu.Lock()
	if l.subs[walletID] == nil {
		l.subs[walletID] = make(map[chan domain.BalanceChanged]struct{})
	}
	l.subs[walletID][ch] = struct{}{}
	l.mu.Unlock()

	go func() {
		<-ctx.Done()
		l.mu.Lock()
		delete(l.subs[walletID], ch)
		if len(l.subs[walletID]) == 0 {
			delete(l.subs, walletID)
		}
		close(ch) // safe: dispatch only sends while holding mu
		l.mu.Unlock()
	}()
	return ch, nil
}
