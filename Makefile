BINARY := jev-guard
PKG := ./cmd/jev-guard
UI_DIR := web
UI_DIST := internal/server/web/dist

.PHONY: build build-go test test-go vet fmt tidy run clean doctor check ui-install ui-build policy-reset macos-app

# Frontend (React + shadcn/ui). Requires Node 20+.
# npm install (not ci): no lockfile is committed since it can't be
# generated offline; switch to `npm ci` + committed lock once you have one.
ui-install:
	cd $(UI_DIR) && npm install

ui-build: ui-install
	cd $(UI_DIR) && npm run build

# Full build: frontend first so go:embed finds web/dist.
build: ui-build
	go build -o $(BINARY) $(PKG)

# Go-only escape hatch (fails loudly at compile time if dist/ is missing).
build-go:
	go build -o $(BINARY) $(PKG)

test: ui-build
	go test ./...

test-go:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l internal cmd; test -z "$$(gofmt -l internal cmd)"

tidy:
	go mod tidy

run: build
	./$(BINARY) serve

# Reset DB policy to bundled YAML defaults. Destroys dashboard custom
# rules (users survive); restart the server afterwards to go live.
policy-reset: build-go
	./$(BINARY) policy reseed --mode replace --force

doctor: build
	./$(BINARY) doctor

# macOS Dock bundle (darwin only): jev-guard.app wrapping ./jev-guard serve.
# Needs web/dist first — on a fresh checkout run `make build` (or ui-build)
# once, then build-go suffices. The baked repo path means re-run after moves.
macos-app: build-go
	@if [ "$$(uname -s)" != "Darwin" ]; then echo "macos-app: macOS only (uname $$(uname -s))"; exit 1; fi
	./scripts/make-macos-app.sh

check: ui-build fmt vet test

clean:
	rm -f $(BINARY)
	rm -rf $(UI_DIST) $(UI_DIR)/node_modules dist
