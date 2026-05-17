# go6asm build targets. See docs/DESIGN.md §5.

GO      ?= go
BIN     ?= bin
WASMOUT ?= web/go6asm.wasm
DIST    ?= dist
# Gzipped wasm budget in bytes (DESIGN §10.1: under 2 MB gzipped).
WASM_BUDGET ?= 2097152

.PHONY: all build test vet wasm wasm-check wasm-size wasm-smoke serve site functest fuzz gen-isa refresh-ca65-fixtures clean

all: test wasm-check wasm-size

# Build every cmd/* binary. None exist yet (Phase 1+).
build:
	@cmds=$$(ls -d cmd/*/ 2>/dev/null || true); \
	if [ -z "$$cmds" ]; then echo "no cmd/* yet"; else \
		mkdir -p $(BIN); \
		for c in $$cmds; do $(GO) build -o $(BIN)/ ./$$c; done; fi

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# Build the wasm assembler binary (Phase 4). No-op until cmd/go6asm-wasm exists.
wasm:
	@if [ -d cmd/go6asm-wasm ]; then \
		GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags="-s -w" -o $(WASMOUT) ./cmd/go6asm-wasm; \
		cp "$$($(GO) env GOROOT)/lib/wasm/wasm_exec.js" web/; \
	else echo "cmd/go6asm-wasm not present yet (Phase 4)"; fi

# Enforce the core-wasm invariant (DESIGN §4): every package must
# cross-compile for js/wasm. CI runs this on every change.
wasm-check:
	GOOS=js GOARCH=wasm $(GO) build ./...

# Enforce the wasm bundle-size budget (DESIGN §10.1). Fails the build if
# the gzipped binary regresses past WASM_BUDGET. Part of `make all`.
wasm-size: wasm
	@sz=$$(gzip -9 -c $(WASMOUT) | wc -c | tr -d ' '); \
	pct=$$(( sz * 100 / $(WASM_BUDGET) )); \
	printf 'wasm: %s B gzipped, budget %s B (%s%%)\n' "$$sz" "$(WASM_BUDGET)" "$$pct"; \
	if [ "$$sz" -gt "$(WASM_BUDGET)" ]; then \
		echo "FAIL: gzipped wasm exceeds the §10.1 budget"; exit 1; fi

# End-to-end: build the wasm binary and exercise go6asm.assemble() from
# JavaScript under Node (DESIGN §15 wasm smoke).
wasm-smoke: wasm
	@command -v node >/dev/null || { echo "node not installed"; exit 1; }
	node web/smoke.js

# Serve the web/ demo locally with the correct application/wasm MIME
# type (instantiateStreaming requires it). Rebuilds the wasm first.
serve: wasm
	cd web && $(GO) run serve.go

# Stage the deployable static site (GitHub Pages). Builds the wasm
# fresh and copies only the runtime files — no serve.go/smoke.js, no
# Go source. `.nojekyll` makes Pages serve the files verbatim. All
# asset paths are relative, so it works at any base URL (project
# Pages serve at /<repo>/). Deployed by .github/workflows/pages.yml;
# or copy $(DIST)/ to /docs and point Pages at the docs folder.
site: wasm
	@rm -rf $(DIST) && mkdir -p $(DIST)
	@cp web/index.html web/reference.html web/go6asm.js \
	    web/asm-worker.js web/go6asm.wasm web/wasm_exec.js $(DIST)/
	@touch $(DIST)/.nojekyll
	@echo "site staged → $(DIST)/ :" && ls -1 $(DIST)

# Regenerate the ISA table from ca65 (requires cl65).
gen-isa:
	@command -v cl65 >/dev/null || { echo "cl65 not installed (brew install cc65)"; exit 1; }
	cd isa && $(GO) generate ./...

# Regenerate compat goldens from ca65 (requires cl65).
refresh-ca65-fixtures:
	@command -v cl65 >/dev/null || { echo "cl65 not installed (brew install cc65)"; exit 1; }
	@echo "ca65 baseline: $$(ca65 --version 2>&1)"
	GO6ASM_UPDATE=1 $(GO) test ./compat/ -run TestCA65Differential

# Execution oracle: assemble with go6asm, run on beevik/go6502. Separate
# module so the emulator dependency stays out of go6asm's graph.
functest:
	cd test/klaus && $(GO) test ./...

fuzz:
	@echo "no fuzz targets yet (Phase 5)"

clean:
	rm -rf $(BIN) $(WASMOUT) web/wasm_exec.js $(DIST)
