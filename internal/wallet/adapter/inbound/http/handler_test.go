package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

// stubService records the caller and returns canned results. Methods not
// overridden panic via the nil embedded interface.
type stubService struct {
	port.WalletService
	gotUser, gotCurrency, gotID string
	gotQuery                    port.EntryQuery
	err                         error
}

func (s *stubService) GetWalletByOwner(_ context.Context, userID, currency string) (*domain.Wallet, error) {
	s.gotUser, s.gotCurrency = userID, currency
	return &domain.Wallet{ID: "me-wallet", OwnerID: userID}, s.err
}

func (s *stubService) GetWallet(_ context.Context, userID, id string) (*domain.Wallet, error) {
	s.gotUser, s.gotID = userID, id
	if s.err != nil {
		return nil, s.err
	}
	return &domain.Wallet{ID: id, OwnerID: userID}, nil
}

func (s *stubService) ListEntries(_ context.Context, userID, walletID string, q port.EntryQuery) ([]domain.HistoryEntry, error) {
	s.gotUser, s.gotID, s.gotQuery = userID, walletID, q
	return nil, s.err
}

func serve(t *testing.T, svc port.WalletService, method, path, user string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), context.Background())
	req := httptest.NewRequest(method, path, nil)
	if user != "" {
		req.Header.Set(userIDHeader, user)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGetMyWalletRoute(t *testing.T) {
	svc := &stubService{}
	rec := serve(t, svc, http.MethodGet, "/wallets/me", "u_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if svc.gotUser != "u_123" || svc.gotCurrency != "THB" || svc.gotID != "" {
		t.Fatalf("routed wrongly: user=%q currency=%q id=%q", svc.gotUser, svc.gotCurrency, svc.gotID)
	}
}

func TestErrorStatuses(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{domain.ErrUnauthenticated, http.StatusUnauthorized},
		{domain.ErrForbidden, http.StatusForbidden},
		{domain.ErrWalletNotFound, http.StatusNotFound},
	}
	for _, c := range cases {
		rec := serve(t, &stubService{err: c.err}, http.MethodGet, "/wallets/abc", "u_123")
		if rec.Code != c.want {
			t.Errorf("%v: status = %d, want %d", c.err, rec.Code, c.want)
		}
	}
}

func TestListEntriesDirection(t *testing.T) {
	svc := &stubService{}
	rec := serve(t, svc, http.MethodGet, "/wallets/abc/entries?direction=out&limit=5", "u_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if svc.gotQuery.Direction != domain.DirectionOut || svc.gotQuery.Limit != 5 {
		t.Fatalf("query = %+v", svc.gotQuery)
	}
	if rec := serve(t, svc, http.MethodGet, "/wallets/abc/entries?direction=sideways", "u_123"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad direction: status = %d, want 400", rec.Code)
	}
}
