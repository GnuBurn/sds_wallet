package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/GnuBurn/sds_wallet/internal/wallet/core/domain"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/port"
)

const (
	idempotencyHeader = "Idempotency-Key"
	maxBodyBytes      = 1 << 20
)

type Handler struct {
	svc port.WalletService
	log *slog.Logger
	// streams is cancelled on server shutdown so long-lived SSE
	// connections end and don't block graceful shutdown.
	streams context.Context
}

func NewHandler(svc port.WalletService, log *slog.Logger, streams context.Context) http.Handler {
	h := &Handler{svc: svc, log: log, streams: streams}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("POST /wallets", h.createWallet)
	mux.HandleFunc("GET /wallets/{id}", h.getWallet)
	mux.HandleFunc("POST /wallets/{id}/deposit", h.deposit)
	mux.HandleFunc("POST /wallets/{id}/withdraw", h.withdraw)
	mux.HandleFunc("GET /wallets/{id}/entries", h.listEntries)
	mux.HandleFunc("GET /wallets/{id}/events", h.streamBalance)
	mux.HandleFunc("POST /transfers", h.transfer)

	return recoverer(log, requestLogger(log, mux))
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) createWallet(w http.ResponseWriter, r *http.Request) {
	var req createWalletRequest
	if !h.decode(w, r, &req) {
		return
	}
	wallet, err := h.svc.CreateWallet(r.Context(), port.CreateWalletCmd{
		OwnerID:  req.OwnerID,
		Currency: req.Currency,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toWalletResponse(wallet))
}

func (h *Handler) getWallet(w http.ResponseWriter, r *http.Request) {
	wallet, err := h.svc.GetWallet(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWalletResponse(wallet))
}

func (h *Handler) deposit(w http.ResponseWriter, r *http.Request) {
	h.moneyOp(w, r, h.svc.Deposit)
}

func (h *Handler) withdraw(w http.ResponseWriter, r *http.Request) {
	h.moneyOp(w, r, h.svc.Withdraw)
}

func (h *Handler) moneyOp(w http.ResponseWriter, r *http.Request,
	op func(context.Context, port.MoneyCmd) (*domain.Transaction, error),
) {
	var req amountRequest
	if !h.decode(w, r, &req) {
		return
	}
	tx, err := op(r.Context(), port.MoneyCmd{
		WalletID:       r.PathValue("id"),
		Amount:         req.Amount,
		IdempotencyKey: r.Header.Get(idempotencyHeader),
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransactionResponse(tx))
}

func (h *Handler) transfer(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if !h.decode(w, r, &req) {
		return
	}
	tx, err := h.svc.Transfer(r.Context(), port.TransferCmd{
		FromWalletID:   req.FromWalletID,
		ToWalletID:     req.ToWalletID,
		Amount:         req.Amount,
		IdempotencyKey: r.Header.Get(idempotencyHeader),
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransactionResponse(tx))
}

func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	entries, err := h.svc.ListEntries(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": toEntryResponses(entries)})
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "request body is required")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		return false
	}
	return true
}

// fail maps domain errors to HTTP status codes.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, domain.ErrWalletNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrWalletExists),
		errors.Is(err, domain.ErrIdempotencyKeyReused):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrInsufficientFunds),
		errors.Is(err, domain.ErrWalletInactive),
		errors.Is(err, domain.ErrCurrencyMismatch),
		errors.Is(err, domain.ErrBalanceOverflow):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrInvalidAmount),
		errors.Is(err, domain.ErrInvalidCurrency),
		errors.Is(err, domain.ErrInvalidOwner),
		errors.Is(err, domain.ErrSameWallet),
		errors.Is(err, domain.ErrMissingIdempotencyKey):
		status = http.StatusBadRequest
	}

	if status == http.StatusInternalServerError {
		h.log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, status, "internal server error")
		return
	}
	writeError(w, status, err.Error())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
