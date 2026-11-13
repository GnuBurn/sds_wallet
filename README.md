# sds_wallet

Wallet microservices in Go, built with hexagonal architecture (ports & adapters).

## Layout

```
cmd/wallet/                         entrypoint: wires adapters into the core
internal/wallet/
  core/                             business logic, no infrastructure imports
    domain/                         Wallet, Transaction, Entry, events, errors
    port/                           interfaces: inbound (use cases) + outbound (repos, events)
    service/                        use-case implementations
  adapter/
    inbound/http/                   REST + SSE handlers  -> port.WalletService
    outbound/postgres/              repositories, unit of work, LISTEN/NOTIFY, migrations
deployments/                        docker-compose + Dockerfile
```

Dependencies only point inward: `adapter -> port <- service -> domain`.
To add a service (e.g. `user`), copy the `internal/wallet` shape into
`internal/user` and add `cmd/user`.

## Run

```sh
# everything in docker
docker compose -f deployments/docker-compose.yml up -d --build

# or: postgres in docker, service locally
docker compose -f deployments/docker-compose.yml up -d wallet-db
DATABASE_URL="postgres://wallet:wallet@localhost:5433/wallet?sslmode=disable" go run ./cmd/wallet
```

PowerShell: `$env:DATABASE_URL="postgres://..."; go run ./cmd/wallet`

Migrations run automatically at startup.

| Env var        | Default                                                          |
|----------------|------------------------------------------------------------------|
| `HTTP_ADDR`    | `:8080`                                                          |
| `DATABASE_URL` | `postgres://wallet:wallet@localhost:5432/wallet?sslmode=disable` |

## API

Amounts are integers in minor units (`10050` = 100.50 THB).
Money-moving requests require an `Idempotency-Key` header; retrying with
the same key returns the original result instead of moving money twice.

**Caller identity.** Every route except `POST /wallets` and `/healthz` needs an
`X-User-Id` header. The API gateway verifies the JWT and sets this header from
the `sub` claim, and it must remove any `X-User-Id` sent by the client. Without
the header the service returns **401**. Using a wallet whose `owner_id` is not
the caller returns **403**. For a transfer, only `from_wallet_id` is checked.

Request bodies are JSON. Unknown fields are rejected with **400**.

| Method | Path | Headers | Success |
|--------|------|---------|---------|
| POST | [`/wallets`](#create-a-wallet) | — | 201 Wallet |
| GET  | [`/wallets/me`](#get-my-wallet) | `X-User-Id` | 200 Wallet |
| GET  | [`/wallets/{id}`](#get-a-wallet) | `X-User-Id` | 200 Wallet |
| POST | [`/wallets/{id}/deposit`](#deposit--withdraw) | `X-User-Id`, `Idempotency-Key` | 200 Transaction |
| POST | [`/wallets/{id}/withdraw`](#deposit--withdraw) | `X-User-Id`, `Idempotency-Key` | 200 Transaction |
| POST | [`/transfers`](#transfer) | `X-User-Id`, `Idempotency-Key` | 200 Transaction |
| GET  | [`/wallets/{id}/entries`](#history) | `X-User-Id` | 200 `{"entries":[…]}` |
| GET  | [`/wallets/{id}/events`](#realtime-balance-updates) | `X-User-Id` | 200 SSE stream |
| GET  | `/healthz` | — | 200 `{"status":"ok"}` |

### Response objects

**Wallet**

```json
{
  "id": "0193a2c4-7f1e-7b3a-9c1e-5d2f8a6b3f9a",
  "owner_id": "u_123",
  "currency": "THB",
  "balance": 124050,
  "status": "active",
  "version": 12,
  "created_at": "2026-11-01T09:00:00Z",
  "updated_at": "2026-11-12T09:10:00Z"
}
```

`status` is `active` or `frozen`. Money can't move in or out of a frozen wallet.
`version` goes up by one on every balance change.

**Transaction**

```json
{
  "id": "0193a2d1-…",
  "idempotency_key": "6f1c2e9a-…",
  "type": "transfer",
  "amount": 15000,
  "currency": "THB",
  "from_wallet_id": "0193a2c4-…",
  "to_wallet_id": "0193a2c9-…",
  "note": "Dinner split",
  "created_at": "2026-11-12T12:43:08Z",
  "entries": [
    { "id": 811, "transaction_id": "0193a2d1-…", "wallet_id": "0193a2c4-…", "amount": -15000, "balance_after": 109050, "created_at": "2026-11-12T12:43:08Z" },
    { "id": 812, "transaction_id": "0193a2d1-…", "wallet_id": "0193a2c9-…", "amount": 15000,  "balance_after": 15000,  "created_at": "2026-11-12T12:43:08Z" }
  ]
}
```

- `type` is `deposit`, `withdrawal` or `transfer`.
- `amount` is always positive. In `entries`, the amount is signed (negative = money out).
- Deposits have no `from_wallet_id` and withdrawals have no `to_wallet_id`. `note` appears only when set.

**Error**

```json
{ "error": "insufficient funds" }
```

| Status | When |
|--------|------|
| 400 | Bad JSON, unknown field, `amount` ≤ 0, invalid currency, empty `owner_id`, same wallet, note too long, missing `Idempotency-Key`, bad `direction` |
| 401 | `X-User-Id` missing |
| 403 | The wallet isn't the caller's |
| 404 | Wallet not found |
| 409 | Wallet already exists for this owner and currency, or `Idempotency-Key` reused with a different body |
| 422 | Insufficient funds, wallet not active, currency mismatch, balance overflow |
| 500 | Unexpected error (`"internal server error"`) |

### Create a wallet

`POST /wallets`. Internal, for provisioning. No caller check.

```json
{ "owner_id": "u_123", "currency": "THB" }
```

Returns **201** with a Wallet whose `balance` is 0. Each owner can have one wallet per currency (**409** otherwise).

### Get my wallet

`GET /wallets/me?currency=THB`. `currency` is optional and defaults to `THB`.
Returns the caller's Wallet, or **404** `{"error":"wallet not found"}`.

### Get a wallet

`GET /wallets/{id}`. Returns a Wallet.

### Deposit / withdraw

`POST /wallets/{id}/deposit` or `POST /wallets/{id}/withdraw`

```json
{ "amount": 50000 }
```

Returns a Transaction with one entry.

```sh
curl -X POST localhost:8080/wallets/$ID/deposit \
  -H 'X-User-Id: u_123' -H 'Idempotency-Key: 6f1c2e9a-…' \
  -d '{"amount":50000}'
```

### Transfer

`POST /transfers`. `from_wallet_id` must belong to the caller. Both wallets must have the same currency.

```json
{
  "from_wallet_id": "0193a2c4-…",
  "to_wallet_id": "0193a2c9-…",
  "amount": 15000,
  "note": "Dinner split"
}
```

`note` is optional: up to 80 characters, with surrounding spaces trimmed.
Returns a Transaction with two entries (sender first, then receiver).

### History

`GET /wallets/{id}/entries?limit=20&offset=0&direction=out`

| Query | Default | |
|-------|---------|-|
| `limit` | 20 | max 100 |
| `offset` | 0 | |
| `direction` | all | `in` (amount > 0) or `out` (amount < 0) |

Entries come newest first:

```json
{
  "entries": [
    {
      "id": 811,
      "transaction_id": "0193a2d1-…",
      "wallet_id": "0193a2c4-…",
      "amount": -15000,
      "balance_after": 109050,
      "created_at": "2026-11-12T12:43:08Z",
      "type": "transfer",
      "note": "Dinner split",
      "counterparty_wallet_id": "0193a2c9-…",
      "counterparty_owner_id": "u_456"
    }
  ]
}
```

`note`, `counterparty_wallet_id` and `counterparty_owner_id` appear only on transfers (and `note` only if it was set).

### Realtime balance updates

`GET /wallets/{id}/events` is a Server-Sent Events stream. It sends the
current balance immediately, then one `balance` event for every committed change:

```
event: balance
data: {"wallet_id":"...","transaction_id":"...","type":"transfer","amount":2500,"balance":2500,"currency":"THB","occurred_at":"..."}
```

Browser: `new EventSource("/wallets/" + id + "/events").addEventListener("balance", e => ...)`
CLI: `curl -N -H 'X-User-Id: alice' localhost:8080/wallets/$ID/events`

How it works: inside the same DB transaction that changes the balance, the
service calls `pg_notify`. Postgres only delivers it after commit, so clients
never see rolled-back balances. Each service instance LISTENs and fans events
out to its SSE clients, so it works with multiple replicas.

When a message broker is added later, implement `port.EventPublisher`
(transactional outbox -> Kafka/NATS) and `port.EventSubscriber`; the core and
HTTP layer do not change.

## Design notes

- Money is `int64` minor units, never floats.
- Every movement writes a `transactions` row plus append-only `ledger_entries`
  with `balance_after`, so history is auditable.
- Wallet rows are locked (`SELECT ... FOR UPDATE`) during a movement; transfers
  lock both wallets in ID order to avoid deadlocks. DB `CHECK (balance >= 0)`
  is a last line of defence.

## Test

```sh
go test ./...            # unit tests; Postgres tests are skipped
make test-integration    # starts the docker postgres and runs everything
```

Postgres tests run only when `TEST_DATABASE_URL` is set. Each test creates its
own schema, migrates it and drops it afterwards, so any scratch database works:

```sh
TEST_DATABASE_URL="postgres://wallet:wallet@localhost:5433/wallet?sslmode=disable" go test ./...
```

PowerShell (no `make` on Windows):

```powershell
docker compose -f deployments/docker-compose.yml up -d wallet-db
$env:TEST_DATABASE_URL="postgres://wallet:wallet@localhost:5433/wallet?sslmode=disable"; go test -count=1 ./...
```

| Package | What it covers |
|---|---|
| `core/domain` | credit/debit rules, frozen wallets, currency parsing |
| `core/service` | use cases against in-memory fakes: idempotency, transfers, ownership checks, notes, history |
| `adapter/inbound/http` | routing, `X-User-Id` / `Idempotency-Key` handling, body validation, error → status mapping, SSE stream |
| `adapter/outbound/postgres` | real SQL: migrations, repositories, history query, rollback, concurrent transfers (no deadlock, money conserved), concurrent same-key requests, LISTEN/NOTIFY delivers only committed events |
