# ACLine: build and install the CLI and the VS Code extension, one step at a time.
#
#   make            show this help
#   make install    build + install the CLI, then build + install the extension
#
# Every step is its own target, so you can run them one by one:
#   make install-cli        build the CLI and put it on your PATH
#   make install-ext        build, package and install the extension into VS Code
#
# Overridable: BINDIR, CODE, VERSION, BACKUP, BACKUP_DIR, STORE (see `make help`).

SHELL := /bin/bash
.DEFAULT_GOAL := help
.NOTPARALLEL:   # the steps must run in order, even under `make -j`

# --- CLI ---------------------------------------------------------------
BINDIR  ?= $(HOME)/.local/bin
BACKUP  ?= 1
VERSION ?=
BIN_OUT := bin/acline
STAMP   := $(shell date +%Y%m%d-%H%M%S)
# Without VERSION the version string comes from the VCS info Go embeds
# (e.g. "acline dev (3c6dbf8, 2026-...)"). With VERSION=v0.3.0 it is stamped in.
LDFLAGS := $(if $(VERSION),-ldflags "-X acline/internal/cmd.version=$(VERSION) -X acline/internal/cmd.commit=$$(git rev-parse --short HEAD) -X acline/internal/cmd.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)",)

# The store `backup-store` copies (ACLINE_DB overrides, as for the CLI), and where the copy
# goes. The backup deliberately lives OUTSIDE the store's own directory: a backup that sits
# next to the store is lost with it.
STORE      ?= $(if $(ACLINE_DB),$(ACLINE_DB),$(HOME)/.acline/store.db)
BACKUP_DIR ?= $(HOME)/.acline-backups

# --- Extension ---------------------------------------------------------
CODE        ?= code
EXT_DIR     := vscode-acline
EXT_NAME    := $(shell node -p "require('./$(EXT_DIR)/package.json').name" 2>/dev/null)
EXT_PUB     := $(shell node -p "require('./$(EXT_DIR)/package.json').publisher" 2>/dev/null)
EXT_VERSION := $(shell node -p "require('./$(EXT_DIR)/package.json').version" 2>/dev/null)
EXT_ID      := $(EXT_PUB).$(EXT_NAME)
VSIX        := $(EXT_DIR)/$(EXT_NAME)-$(EXT_VERSION).vsix
VSCE        ?= npx --yes @vscode/vsce

.PHONY: help all build install check vuln generate cli install-cli backup-store ext-deps ext-build ext-test ext-package install-ext versions clean

help: ## Show this help
	@echo "ACLine build and install"
	@echo
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  make %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "Variables:"
	@echo "  BINDIR=$(BINDIR)   where install-cli puts the binary"
	@echo "  BACKUP=$(BACKUP)                  1 keeps the old binary as acline.bak-<stamp>; 0 skips that"
	@echo "  BACKUP_DIR=$(BACKUP_DIR)   where backup-store writes"
	@echo "  VERSION=            stamp a release version into the CLI (default: VCS info)"
	@echo "  CODE=$(CODE)             the VS Code CLI used by install-ext"
	@echo
	@echo "Extension: $(EXT_ID) $(EXT_VERSION)  ->  $(VSIX)"

all: build ## Build the CLI and package the extension (installs nothing)
build: cli ext-package

install: install-cli install-ext ## Install the CLI, then the extension, one after the other
	@echo
	@echo "Done. Restart VS Code (or run 'Developer: Reload Window') so the extension starts the new acline."

# Same steps as .github/workflows/ci.yml. Packages are `./internal/... .` (not `./...`) so
# vscode-acline/node_modules is never walked.
check: ## Run vet, race-detector tests, staticcheck and the extension tests (what CI runs)
	go vet ./internal/... .
	go test -race -count=1 ./internal/... .
	go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./internal/... .
	$(MAKE) ext-test

vuln: ## Scan Go dependencies for known vulnerabilities (govulncheck)
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./internal/... .

# --- CLI ---------------------------------------------------------------
generate: ## Regenerate the extension's TypeScript types from the MCP server's structs
	go generate ./internal/mcp

cli: ## Build the CLI into ./bin/acline
	@mkdir -p bin
	go build $(LDFLAGS) -o $(BIN_OUT) .
	@echo "built $(BIN_OUT): $$($(BIN_OUT) version)"

install-cli: cli ## Build the CLI and install it to BINDIR (atomic; keeps the old binary as a backup)
	@mkdir -p "$(BINDIR)"
	@if [ "$(BACKUP)" = "1" ] && [ -e "$(BINDIR)/acline" ]; then \
		cp -p "$(BINDIR)/acline" "$(BINDIR)/acline.bak-$(STAMP)" && echo "kept the previous binary as $(BINDIR)/acline.bak-$(STAMP)"; \
	fi
	install -m 0755 $(BIN_OUT) "$(BINDIR)/.acline.new"
	mv -f "$(BINDIR)/.acline.new" "$(BINDIR)/acline"
	@echo "installed: $$("$(BINDIR)/acline" version)"
	@echo "note: a newer acline migrates your store the first time it opens it, and an older acline then refuses it."
	@echo "      'make backup-store' copies it (to $(BACKUP_DIR)) first."
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "warning: $(BINDIR) is not on your PATH";; esac

backup-store: ## Back up the acline store to BACKUP_DIR (~/.acline-backups), outside the store's directory
	@test -f "$(STORE)" || { echo "no store at $(STORE)"; exit 1; }
	@mkdir -p "$(BACKUP_DIR)"
	@if command -v sqlite3 >/dev/null; then \
		sqlite3 "$(STORE)" ".backup '$(BACKUP_DIR)/store.db.$(STAMP)'" && echo "backed up (consistent SQLite backup) to $(BACKUP_DIR)/store.db.$(STAMP)"; \
	else \
		cp -p "$(STORE)" "$(BACKUP_DIR)/store.db.$(STAMP)"; \
		[ ! -s "$(STORE)-wal" ] || cp -p "$(STORE)-wal" "$(BACKUP_DIR)/store.db.$(STAMP)-wal"; \
		echo "backed up (file copy; sqlite3 not found) to $(BACKUP_DIR)/store.db.$(STAMP)"; \
	fi

# --- Extension ---------------------------------------------------------
ext-deps: ## Install the extension's npm dependencies if they are missing
	@if [ ! -d "$(EXT_DIR)/node_modules" ]; then cd $(EXT_DIR) && npm ci; else echo "$(EXT_DIR)/node_modules present"; fi

ext-build: generate ext-deps ## Compile the extension (regenerates its types first)
	cd $(EXT_DIR) && npm run compile

ext-test: ext-deps ## Run the extension's unit tests
	cd $(EXT_DIR) && npm test

ext-package: ext-build ## Package the extension as a .vsix in vscode-acline/
	cd $(EXT_DIR) && $(VSCE) package --no-dependencies --allow-missing-repository --out $(notdir $(VSIX))
	@echo "packaged $(VSIX)"

install-ext: ext-package ## Package the extension and install it into VS Code (replaces the installed version)
	@command -v $(CODE) >/dev/null || { echo "'$(CODE)' not found: in VS Code run 'Shell Command: Install code command in PATH', or set CODE=/path/to/code"; exit 1; }
	$(CODE) --install-extension $(VSIX) --force
	@echo "installed $(EXT_ID) $(EXT_VERSION) into VS Code"

# --- Misc ---------------------------------------------------------------
versions: ## Show what is installed, and what a build would produce
	@echo "installed CLI : $$(command -v acline >/dev/null && acline version || echo none)"
	@echo "built CLI     : $$(test -x $(BIN_OUT) && $(BIN_OUT) version || echo 'not built')"
	@echo "extension     : $(EXT_ID) $(EXT_VERSION) (source)"
	@echo "in VS Code    : $$($(CODE) --list-extensions --show-versions 2>/dev/null | grep -i '$(EXT_NAME)' || echo 'not installed')"

clean: ## Remove ./bin and the extension's build output and packages
	rm -rf bin $(EXT_DIR)/out $(EXT_DIR)/*.vsix
