package http

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

// moneyStub records money commands and returns a canned transaction.
type moneyStub struct {
	stubService
	gotMoney    port.MoneyCmd
	gotTransfer port.TransferCmd
	op          string
	events      chan domain.BalanceChanged
	subErr      error
}

func (s *moneyStub) Deposit(_ context.Context, cmd port.MoneyCmd) (*domain.Transaction, error) {
	s.op, s.gotMoney = "deposit", cmd
	if s.err != nil {
		return nil, s.err
	}
	return &domain.Transaction{ID: "tx1", Type: domain.TxDeposit, Amount: cmd.Amount, ToWalletID: cmd.WalletID}, nil
}

func (s *moneyStub) Withdraw(_ context.Context, cmd port.MoneyCmd) (*domain.Transaction, error) {
	s.op, s.gotMoney = "withdraw", cmd
	if s.err != nil {
		return nil, s.err
	}
	return &domain.Transaction{ID: "tx1", Type: domain.TxWithdrawal, Amount: cmd.Amount, FromWalletID: cmd.WalletID}, nil
}

func (s *moneyStub) Transfer(_ context.Context, cmd port.TransferCmd) (*domain.Transaction, error) {
	s.op, s.gotTransfer = "transfer", cmd
	if s.err != nil {
		return nil, s.err
	}
	return &domain.Transaction{ID: "tx1", Type: domain.TxTransfer, Amount: cmd.Amount, Note: cmd.Note,
		FromWalletID: cmd.FromWalletID, ToWalletID: cmd.ToWalletID}, nil
}

func (s *moneyStub) SubscribeBalance(_ context.Context, userID, walletID string) (<-chan domain.BalanceChanged, error) {
	s.gotUser, s.gotID = userID, walletID
	return s.events, s.subErr
}

func send(t *testing.T, svc port.WalletService, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), context.Background())
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMoneyRoutesPassCallerAndKey(t *testing.T) {
	headers := map[string]string{userIDHeader: " u_123 ", idempotencyHeader: "key-1"}
	for _, op := range []string{"deposit", "withdraw"} {
		svc := &moneyStub{}
		rec := send(t, svc, http.MethodPost, "/wallets/w1/"+op, `{"amount":500}`, headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", op, rec.Code, rec.Body)
		}
		want := port.MoneyCmd{UserID: "u_123", WalletID: "w1", Amount: 500, IdempotencyKey: "key-1"}
		if svc.op != op || svc.gotMoney != want {
			t.Fatalf("%s: got %s %+v, want %+v", op, svc.op, svc.gotMoney, want)
		}
	}

	svc := &moneyStub{}
	rec := send(t, svc, http.MethodPost, "/transfers",
		`{"from_wallet_id":"w1","to_wallet_id":"w2","amount":150,"note":"Dinner split"}`, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("transfer: status = %d, body = %s", rec.Code, rec.Body)
	}
	want := port.TransferCmd{UserID: "u_123", FromWalletID: "w1", ToWalletID: "w2", Amount: 150, Note: "Dinner split", IdempotencyKey: "key-1"}
	if svc.gotTransfer != want {
		t.Fatalf("transfer cmd = %+v, want %+v", svc.gotTransfer, want)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["note"] != "Dinner split" || body["type"] != "transfer" || body["amount"] != float64(150) {
		t.Fatalf("response body = %v", body)
	}
}

func TestRequestBodyValidation(t *testing.T) {
	cases := []struct{ name, body string }{
		{"empty body", ``},
		{"bad json", `{"amount":`},
		{"unknown field", `{"from_wallet_id":"w1","to_wallet_id":"w2","amount":1,"memo":"x"}`},
		{"wrong type", `{"from_wallet_id":"w1","to_wallet_id":"w2","amount":"100"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &moneyStub{}
			rec := send(t, svc, http.MethodPost, "/transfers", c.body, map[string]string{userIDHeader: "u_1"})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if svc.op != "" {
				t.Fatal("service was called with an invalid body")
			}
		})
	}
}

func TestMoneyErrorStatuses(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{domain.ErrInsufficientFunds, http.StatusUnprocessableEntity},
		{domain.ErrWalletInactive, http.StatusUnprocessableEntity},
		{domain.ErrCurrencyMismatch, http.StatusUnprocessableEntity},
		{domain.ErrIdempotencyKeyReused, http.StatusConflict},
		{domain.ErrMissingIdempotencyKey, http.StatusBadRequest},
		{domain.ErrSameWallet, http.StatusBadRequest},
		{domain.ErrNoteTooLong, http.StatusBadRequest},
		{domain.ErrInvalidAmount, http.StatusBadRequest},
		{domain.ErrUnauthenticated, http.StatusUnauthorized},
		{domain.ErrForbidden, http.StatusForbidden},
		{errors.New("db down"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		rec := send(t, &moneyStub{stubService: stubService{err: c.err}}, http.MethodPost, "/transfers",
			`{"from_wallet_id":"w1","to_wallet_id":"w2","amount":1}`, map[string]string{userIDHeader: "u_1"})
		if rec.Code != c.want {
			t.Errorf("%v: status = %d, want %d", c.err, rec.Code, c.want)
		}
		var body errorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if c.want == http.StatusInternalServerError {
			if body.Error != "internal server error" {
				t.Errorf("500 leaked error details: %q", body.Error)
			}
		} else if body.Error != c.err.Error() {
			t.Errorf("%v: error body = %q", c.err, body.Error)
		}
	}
}

func TestBalanceStream(t *testing.T) {
	svc := &moneyStub{events: make(chan domain.BalanceChanged, 1)}
	streams, closeStreams := context.WithCancel(context.Background())
	defer closeStreams()
	srv := httptest.NewServer(NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), streams))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/wallets/w1/events", nil)
	req.Header.Set(userIDHeader, "u_123")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status = %d, content-type = %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if svc.gotUser != "u_123" || svc.gotID != "w1" {
		t.Fatalf("subscribed as user=%q wallet=%q", svc.gotUser, svc.gotID)
	}

	reader := bufio.NewReader(resp.Body)
	readEvent := func() balanceEventResponse {
		t.Helper()
		var name, data string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read stream: %v", err)
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				if name != "balance" {
					t.Fatalf("event name = %q", name)
				}
				var e balanceEventResponse
				if err := json.Unmarshal([]byte(data), &e); err != nil {
					t.Fatal(err)
				}
				return e
			}
		}
	}

	// The first event is the current balance snapshot, with no transaction.
	if snap := readEvent(); snap.WalletID != "w1" || snap.TransactionID != "" {
		t.Fatalf("snapshot = %+v", snap)
	}

	svc.events <- domain.BalanceChanged{WalletID: "w1", TransactionID: "tx9", Type: domain.TxTransfer, Amount: -250, Balance: 750, Currency: "THB"}
	if e := readEvent(); e.TransactionID != "tx9" || e.Amount != -250 || e.Balance != 750 || e.Type != "transfer" {
		t.Fatalf("event = %+v", e)
	}

	// Server shutdown ends the stream.
	closeStreams()
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
}

func TestBalanceStreamForbidden(t *testing.T) {
	svc := &moneyStub{subErr: domain.ErrForbidden}
	rec := serve(t, svc, http.MethodGet, "/wallets/w1/events", "u_other")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
