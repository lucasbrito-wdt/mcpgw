# Windows hosts produce montyrs-worker.exe.
EXE = $(if $(filter Windows_NT,$(OS)),.exe)

.PHONY: admin worker build test-admin-web test lint fmt

# Builds the static export embedded by web/admin/embed.go. next build wipes out/, so .keep is restored.
admin:
	cd web/admin && pnpm install --frozen-lockfile && pnpm build
	touch web/admin/out/.keep

# The montyrs worker is a separate Rust binary; MCPGW_SANDBOX_ENGINE=monty finds it next to mcpgw.
worker:
	cargo build --release --manifest-path internal/sandbox/montyrs/worker/Cargo.toml
	mkdir -p bin
	cp internal/sandbox/montyrs/worker/target/release/montyrs-worker$(EXE) bin/montyrs-worker$(EXE)
	# The worker runs inside an AppContainer on Windows, which needs read+execute on the exe.
	$(if $(EXE),icacls bin/montyrs-worker.exe /grant "*S-1-15-2-1:(RX)")

build: admin worker
	go build -o bin/mcpgw ./cmd/mcpgw

test-admin-web:
	cd web/admin && pnpm test

# Every Go file except vendored ones under node_modules. third_party/qjs is gofmt-clean, so it stays in.
GO_FILES = $(shell find . -name node_modules -prune -o -name '*.go' -print)

test:
	go test ./...

# gofmt -l exits 0 even when it lists files, so its output is checked explicitly.
lint:
	@unformatted="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed (run make fmt):"; echo "$$unformatted"; exit 1; fi
	go vet ./...

fmt:
	gofmt -w $(GO_FILES)
