COMPOSE := docker compose -f deployments/docker-compose.yml
DB_URL  ?= postgres://wallet:wallet@localhost:5433/wallet?sslmode=disable

.PHONY: up down db run test test-integration vet logs

up:     ## start postgres + wallet service in docker
	$(COMPOSE) up -d --build

down:   ## stop everything
	$(COMPOSE) down

db:     ## start only postgres (for running the service locally)
	$(COMPOSE) up -d wallet-db

run:    ## run the wallet service locally against the docker postgres
	DATABASE_URL="$(DB_URL)" go run ./cmd/wallet

test:   ## unit tests (Postgres tests are skipped)
	go test ./...

test-integration: db  ## all tests, including Postgres tests against the docker postgres
	TEST_DATABASE_URL="$(DB_URL)" go test -count=1 ./...

vet:
	go vet ./...

logs:
	$(COMPOSE) logs -f wallet
