# go6asm

A 6502 cross-assembler in Go: a strict ca65-compatible syntax subset, a
relocatable object format, a separable linker, and first-class WASM support
for in-browser editors and simulators.

**▶ Live playground:** <https://carledwards.github.io/go6asm/> — the
assembler, linker and static analyzer, running entirely in your browser
(WASM, no server).

**Status:** Phases 1–3 complete and byte-verified against cc65 (ca65 + ld65),
Klaus 6502 functional test passing; Phase 4 in progress. The design is in
[`docs/DESIGN.md`](docs/DESIGN.md); read it before touching code.

## Layout

Module-root packages, named after the Go standard-library toolchain
(`go/token`, `go/scanner`, `go/ast`, `go/parser`, `go/constant`) rather
than a `pkg/` tree.

| Package   | Role                                                          |
|-----------|---------------------------------------------------------------|
| `token`   | positions, file set, and the lexical token vocabulary         |
| `scanner` | hand-written tokenizer over `token` (error-recovering)        |
| `diag`    | shared `Diagnostic` + §14 caret renderer                      |
| `isa`     | instruction tables / addressing modes (NMOS 6502 at v1)       |
| `expr`    | 32-bit ca65-parity expression value (`go/constant` analog)    |

Every package compiles under `GOOS=js GOARCH=wasm` and touches no
filesystem — the core-wasm invariant (DESIGN §4). `internal/` is reserved
for genuinely private helpers.

## Make targets

```sh
make test                 # go test ./...
make build                # build cmd/go6asm
make wasm                  # build web/go6asm.wasm (the in-browser assembler)
make wasm-check            # cross-compile ./... for js/wasm (core-wasm invariant)
make wasm-smoke            # build wasm + exercise go6asm.assemble() under Node
make serve                 # build wasm + serve web/ locally (correct wasm MIME)
make site                  # stage the deployable static site into dist/
make functest              # assemble with go6asm, run on beevik/go6502
make refresh-ca65-fixtures # regenerate compat goldens from ca65 (needs cl65)
make gen-isa               # regenerate the ISA table from ca65 (needs cl65)
```

`web/` is the reference in-browser editor: `make serve`, then open
<http://localhost:8080/> — the assembler, linker and static analyzer run
entirely client-side (wasm, no server). Run the canonical Klaus ROM
through the executor with
`GO6ASM_KLAUS_BIN=/path/to/6502_functional_test.bin make functest` (the
GPLv3 ROM is never vendored here).

## Deploying the playground (GitHub Pages)

The `.wasm` is a build artifact and is **not** committed (it's
`.gitignore`d). Deployment builds it in CI:

1. Repo **Settings → Pages → Build and deployment → Source: GitHub
   Actions** (one-time).
2. Push to `main` (or run the workflow manually). `.github/workflows/
   pages.yml` runs `make wasm-check` + `make wasm-smoke` as a gate,
   then `make site`, and publishes `dist/`.

The site is served at `https://<user>.github.io/<repo>/`. All asset
paths are relative and a `.nojekyll` marker is included, so it works at
that sub-path with no extra configuration. To preview the exact
deployable bytes locally: `make site` then serve `dist/`.

Prefer a branch/folder deploy instead of Actions? `make site` and copy
`dist/` to `docs/` on `main`, then point Pages at the `docs` folder.

## License

MIT — see [`LICENSE`](LICENSE).
