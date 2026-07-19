.PHONY: run dev build lint clean

# === Unified Core (Go + HTMX) ===

run:
	@echo "Building and running FastERP Core..."
	go run ./main.go

build:
	@echo "Building FastERP Core binary..."
	go build -o ../../bin/fasterp-core ./main.go
	@echo "Binary at ../../bin/fasterp-core"

dev:
	@echo "FastERP Core development mode"
	@echo "1. Make sure PostgreSQL is running"
	@echo "2. Run: make run"
	@echo ""
	@echo "Frontend is served via HTMX templates (no build step needed)"

# === Lint & Tidy ===

lint:
	go vet ./...

tidy:
	go mod tidy

# === Clean ===

clean:
	rm -rf ../../bin/fasterp-core
	rm -f .default-tenant-id

# === Docker ===

docker-build:
	docker build -t fasterp-core:latest .

docker-run:
	docker run -p 7071:7071 fasterp-core:latest
