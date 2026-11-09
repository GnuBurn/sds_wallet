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

| Method | Path                          | Body                                            |
|--------|-------------------------------|-------------------------------------------------|
| POST   | `/wallets`                    | `{"owner_id":"alice","currency":"THB"}`         |
| GET    | `/wallets/{id}`               |                                                 |
| POST   | `/wallets/{id}/deposit`       | `{"amount":10000}`                              |
| POST   | `/wallets/{id}/withdraw`      | `{"amount":500}`                                |
| POST   | `/transfers`                  | `{"from_wallet_id":"..","to_wallet_id":"..","amount":2500}` |
| GET    | `/wallets/{id}/entries?limit=20&offset=0` | ledger history, newest first        |
| GET    | `/wallets/{id}/events`        | **realtime** balance stream (SSE)               |
| GET    | `/healthz`                    |                                                 |

```sh
curl -X POST localhost:8080/wallets/$ID/deposit \
  -H 'Idempotency-Key: 6f1c...' -d '{"amount":10000}'
```

### Realtime balance updates

`GET /wallets/{id}/events` is a Server-Sent Events stream. It sends the
current balance immediately, then one `balance` event for every committed change:

```
event: balance
data: {"wallet_id":"...","transaction_id":"...","type":"transfer","amount":2500,"balance":2500,"currency":"THB","occurred_at":"..."}
```

Browser: `new EventSource("/wallets/" + id + "/events").addEventListener("balance", e => ...)`
CLI: `curl -N localhost:8080/wallets/$ID/events`

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
go test ./...
```
