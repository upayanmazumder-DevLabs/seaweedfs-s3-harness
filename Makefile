SHELL := /bin/bash
GO ?= go
COMPOSE_FILE := deploy/compose/docker-compose.yaml
TIER ?= standard # smoke | standard | all
PKGS := ./tests/...

.PHONY: build test-unit secrets compose-up compose-down test compose-test \
        vet fmt lint janitor clean

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -l .

test-unit:
	$(GO) test ./internal/...

secrets:
	./scripts/gen-secrets.sh .secrets

compose-up: secrets
	docker compose -f $(COMPOSE_FILE) up -d --wait

compose-down:
	docker compose -f $(COMPOSE_FILE) down -v

# Runs the integration suite against whatever target is described by the
# currently-exported SWFSIT_* env vars (see targets/*.env.example).
test:
	$(GO) test -count=1 -v $(PKGS)

# Convenience: bring up compose, load its env, run the suite, leave the
# stack running for inspection (use `make compose-down` after).
compose-test: compose-up
	set -a && source .secrets/compose.env && set +a && $(GO) test -count=1 -v $(PKGS)

janitor:
	set -a && source .secrets/compose.env && set +a && \
	$(GO) run ./cmd/swfsit janitor

clean: compose-down
	rm -rf bin .secrets artifacts
