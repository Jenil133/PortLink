include .env
# Recipes for the Go services run from server/. Override on the command line, for example:
# make feeder FEED_SOURCE=ais AIS_CSV_PATH=../data/AIS_2024_07_01.csv
AIS_CSV_PATH ?= ../data/$(AIS_FILE)
export

.PHONY: up down infra migrate refresh proto feeder ingest gateway web backfill backfill-docker psql test test-server test-web loadgen fetch-ais

up:               ## build and start the whole stack (migrate waits for TimescaleDB itself)
	docker compose up -d nats timescaledb
	$(MAKE) migrate
	docker compose up -d --build

down:
	docker compose down

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

refresh:          ## materialize the aggregates right now instead of waiting for the policies
	@for v in samples_1s samples_1m samples_10m; do \
	  for i in $$(seq 1 30); do \
	    out=$$(docker compose exec -T timescaledb psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) \
	      -c "CALL refresh_continuous_aggregate('$$v', now() - INTERVAL '8 hours', now() - INTERVAL '10 minutes');" 2>&1) && break; \
	    case "$$out" in *"due to a concurrent refresh"*) [ $$i -lt 30 ] || { echo "$$out"; exit 1; } ;; *) echo "$$out"; exit 1 ;; esac; \
	    echo "$$v: a refresh is already running, retrying"; \
	    sleep 2; \
	  done; \
	  echo "$$v: refreshed"; \
	done

proto:            ## regenerate Go and TS code from proto/
	cd web && PATH="$$(go env GOPATH)/bin:$$PATH" npm run gen

feeder:
	cd server && go run ./cmd/feeder

backfill:         ## one-shot: publish the previous 6 hours, then exit
	cd server && go run ./cmd/feeder -backfill=6h

backfill-docker:  ## one-shot 6 h backfill inside the stack, then materialize aggregates (starts what it needs)
	docker compose up -d nats timescaledb
	$(MAKE) migrate
	docker compose up -d ingest
	docker compose run --rm feeder feeder -backfill=6h
	$(MAKE) refresh

ingest:
	cd server && go run ./cmd/ingest

gateway:
	cd server && go run ./cmd/gateway

web:
	cd web && npm run dev

psql:
	docker compose exec timescaledb psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

test: test-server test-web

test-server:
	cd server && go vet ./... && go test ./...

test-web:
	cd web && npx tsc --noEmit && npm test

loadgen:          ## for other flags: cd server && go run ./cmd/loadgen -firehose
	cd server && go run ./cmd/loadgen -clients 50 -duration 30s

fetch-ais:        ## download one day of AIS data into data/
	./scripts/fetch_ais.sh
