include .env
export

.PHONY: infra migrate proto feeder ingest gateway web backfill psql

infra:            ## start NATS and TimescaleDB
	docker compose up -d nats timescaledb

migrate:          ## wait for TimescaleDB, then apply every SQL file in order (all are idempotent)
	@for i in $$(seq 1 30); do \
	  docker compose exec -T timescaledb pg_isready -q -h 127.0.0.1 -U $(POSTGRES_USER) -d $(POSTGRES_DB) && break; \
	  if [ $$i -eq 30 ]; then echo "TimescaleDB is not ready"; exit 1; fi; \
	  sleep 1; \
	done
	@for f in db/migrations/*.sql; do \
	  echo "applying $$f"; \
	  docker compose exec -T timescaledb psql -v ON_ERROR_STOP=1 -U $(POSTGRES_USER) -d $(POSTGRES_DB) < $$f || exit 1; \
	done

proto:            ## regenerate Go and TS code from proto/
	cd web && PATH="$$(go env GOPATH)/bin:$$PATH" npm run gen

feeder:
	cd server && go run ./cmd/feeder

backfill:         ## one-shot: publish the previous 6 hours, then exit
	cd server && go run ./cmd/feeder -backfill=6h

ingest:
	cd server && go run ./cmd/ingest

gateway:
	cd server && go run ./cmd/gateway

web:
	cd web && npm run dev

psql:
	docker compose exec timescaledb psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)
