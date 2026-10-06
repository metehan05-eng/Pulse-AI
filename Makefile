# PulseAI Makefile
BINARY  := pulse
PKG     := ./cmd/pulseai/
LDFLAGS := -ldflags="-s -w"

.PHONY: build run test lint clean install

build:
	go build $(LDFLAGS) -o $(BINARY) $(PKG)

run: build
	./$(BINARY)

test:
	go test ./internal/... -v -timeout 120s

lint:
	@which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run ./...

clean:
	rm -f $(BINARY)

install: build
	install -m 755 $(BINARY) /usr/local/bin/$(BINARY)

# CLI shortcuts
status:
	./$(BINARY) status

models:
	./$(BINARY) models

analyze:
	@if [ -z "$(PID)" ]; then echo "Usage: make analyze PID=<pid>"; exit 1; fi
	./$(BINARY) analyze --pid $(PID)
