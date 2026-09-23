# Local gate (ADR 0001). `make verify` is what the pre-push hook runs.

GOLANGCI_LINT_VERSION := 2.13.2

BIN           := $(CURDIR)/bin
GOLANGCI_LINT := $(BIN)/golangci-lint
NOTES_REFSPEC := +refs/notes/*:refs/notes/*

.PHONY: setup setup-git setup-lint test lint verify

# Idempotent: safe to run again at any time.
setup: setup-git setup-lint
	go mod download

setup-git:
	git config core.hooksPath .githooks
	git config notes.rewriteRef refs/notes/commits
	@git config --get-all remote.origin.fetch | grep -qxF '$(NOTES_REFSPEC)' || \
		git config --add remote.origin.fetch '$(NOTES_REFSPEC)'

# Installs the pinned binary into ./bin, not through the go.mod tool directive.
setup-lint:
	@if $(GOLANGCI_LINT) version 2>/dev/null | grep -q 'has version $(GOLANGCI_LINT_VERSION) '; then \
		echo 'golangci-lint $(GOLANGCI_LINT_VERSION) already installed'; \
	else \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/v$(GOLANGCI_LINT_VERSION)/install.sh | \
			sh -s -- -b $(BIN) v$(GOLANGCI_LINT_VERSION); \
	fi

test:
	go test -race ./...

lint:
	@test -x $(GOLANGCI_LINT) || { echo 'golangci-lint not found: run make setup' >&2; exit 1; }
	$(GOLANGCI_LINT) run ./...

# Lint, then test, in that order even under -j.
verify:
	@$(MAKE) --no-print-directory lint
	@$(MAKE) --no-print-directory test
