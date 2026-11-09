CREATE TABLE wallets (
    id          UUID PRIMARY KEY,
    owner_id    TEXT        NOT NULL,
    currency    CHAR(3)     NOT NULL,
    balance     BIGINT      NOT NULL DEFAULT 0 CHECK (balance >= 0),
    status      TEXT        NOT NULL,
    version     BIGINT      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    CONSTRAINT wallets_owner_currency_key UNIQUE (owner_id, currency)
);

CREATE TABLE transactions (
    id               UUID PRIMARY KEY,
    idempotency_key  TEXT        NOT NULL,
    type             TEXT        NOT NULL,
    amount           BIGINT      NOT NULL CHECK (amount > 0),
    currency         CHAR(3)     NOT NULL,
    from_wallet_id   UUID REFERENCES wallets (id),
    to_wallet_id     UUID REFERENCES wallets (id),
    created_at       TIMESTAMPTZ NOT NULL,
    CONSTRAINT transactions_idempotency_key_key UNIQUE (idempotency_key)
);

-- Append-only ledger: one row per wallet affected by a transaction.
CREATE TABLE ledger_entries (
    id              BIGSERIAL PRIMARY KEY,
    transaction_id  UUID        NOT NULL REFERENCES transactions (id),
    wallet_id       UUID        NOT NULL REFERENCES wallets (id),
    amount          BIGINT      NOT NULL,
    balance_after   BIGINT      NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX ledger_entries_wallet_idx ON ledger_entries (wallet_id, id DESC);
CREATE INDEX ledger_entries_transaction_idx ON ledger_entries (transaction_id);
