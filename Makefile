BINARY := pgbot
VERSION ?= dev

.PHONY: build test vet lint fmt clean tidy install

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/pgbot

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

install: build
	install -m 0755 bin/$(BINARY) $(or $(PGBOT_INSTALL_DIR),/usr/local/bin)/$(BINARY)

clean:
	rm -rf bin

# Full collector matrix against PG 13-18 (needs docker).
matrix:
	docker compose -f docker-compose.test.yml up -d
	@echo "point PGBOT_TEST_DSN at each service and run: go test ./internal/collect/ -run Integration"
