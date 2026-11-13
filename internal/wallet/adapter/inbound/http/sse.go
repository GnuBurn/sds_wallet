package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
)

const heartbeatInterval = 15 * time.Second

// streamBalance pushes realtime balance updates using Server-Sent Events.
//
//	GET /wallets/{id}/events
//
// It first sends a "balance" event with the current balance, then one
// "balance" event per committed change. Browsers can consume it with
// `new EventSource(url)`.
func (h *Handler) streamBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	walletID := r.PathValue("id")
	user := userID(r)

	// Subscribe before reading the snapshot so no change is missed in between.
	events, err := h.svc.SubscribeBalance(ctx, user, walletID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	wallet, err := h.svc.GetWallet(ctx, user, walletID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // stream has no write timeout

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	w.WriteHeader(http.StatusOK)

	send := func(e balanceEventResponse) error {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: balance\ndata: %s\n\n", data); err != nil {
			return err
		}
		return rc.Flush()
	}

	snapshot := toBalanceEventResponse(domain.BalanceChanged{
		WalletID:   wallet.ID,
		Balance:    wallet.Balance,
		Currency:   wallet.Currency,
		OccurredAt: wallet.UpdatedAt,
	})
	if err := send(snapshot); err != nil {
		return
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.streams.Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			if err := send(toBalanceEventResponse(e)); err != nil {
				return
			}
		case <-heartbeat.C:
			// Comment line keeps proxies/load balancers from closing idle streams.
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
