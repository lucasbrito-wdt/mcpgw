.PHONY: admin build test-admin-web test lint fmt

# Builds the static export embedded by web/admin/embed.go. next build wipes out/, so .keep is restored.
admin:
	cd web/admin && pnpm install --frozen-lockfile && pnpm build
	touch web/admin/out/.keep

build: admin
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
