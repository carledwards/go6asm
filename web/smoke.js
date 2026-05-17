// Node end-to-end smoke for the wasm build (DESIGN §15): instantiate
// go6asm.wasm, call go6asm.assemble() from JS, verify the bytes match
// the native assembler. Run via `make wasm-smoke`.
"use strict";
const fs = require("fs");
const path = require("path");

require(path.join(__dirname, "wasm_exec.js")); // defines globalThis.Go

(async () => {
  const go = new globalThis.Go();
  const bytes = fs.readFileSync(path.join(__dirname, "go6asm.wasm"));
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(instance); // runs main(): sets globalThis.go6asm, then blocks
  await new Promise((r) => setTimeout(r, 0)); // let main reach the block

  const r = globalThis.go6asm.assemble({
    source: "reset:\n\tLDX #$00\nloop:\n\tINX\n\tBNE loop\n\tRTS\n",
    origin: 0x0600,
  });

  if (!r.success) {
    console.error("assemble failed:", JSON.stringify(r.errors, null, 2));
    process.exit(1);
  }
  const hex = [...r.bytes].map((b) => b.toString(16).padStart(2, "0")).join(" ");
  const want = "a2 00 e8 d0 fd 60";
  if (hex !== want) {
    console.error(`byte mismatch: got [${hex}] want [${want}]`);
    process.exit(1);
  }
  if (r.origin !== 0x0600) {
    console.error(`origin ${r.origin.toString(16)}, want 600`);
    process.exit(1);
  }
  // Error path: bad mnemonic must report a structured diagnostic.
  const bad = globalThis.go6asm.assemble({ source: "\tLDQ #1\n" });
  if (bad.success || !bad.errors.length || bad.errors[0].code !== "E0001") {
    console.error("expected E0001 for bad mnemonic, got:", JSON.stringify(bad.errors));
    process.exit(1);
  }

  // Object/link parity: target links via the built-in memory map, obj
  // returns the .o6, and the sidecars come back populated.
  const lk = globalThis.go6asm.assemble({
    source: ".export start\nstart:\n\tLDA data\n\tJMP start\ndata:\n\t.byte $7E\n",
    target: "flat-64k",
    obj: true,
  });
  if (!lk.success) {
    console.error("link assemble failed:", JSON.stringify(lk.errors, null, 2));
    process.exit(1);
  }
  const lhex = [...lk.bytes].map((b) => b.toString(16).padStart(2, "0")).join(" ");
  if (lhex !== "ad 06 02 4c 00 02 7e" || lk.origin !== 0x0200) {
    console.error(`link mismatch: [${lhex}] origin ${lk.origin.toString(16)}`);
    process.exit(1);
  }
  if (!lk.object || lk.object.length < 4 ||
      String.fromCharCode(...lk.object.slice(0, 4)) !== "GO6O") {
    console.error(`expected a GO6O object, got ${lk.object && lk.object.length} bytes`);
    process.exit(1);
  }
  if (!lk.lbl.includes("al C:$0200 .start") ||
      !JSON.parse(lk.sym).symbols.some((s) => s.name === "start")) {
    console.error("sidecars missing expected content:", lk.lbl, lk.sym);
    process.exit(1);
  }

  // Per-PC comments (DESIGN §10.2): keyed "0xADDR" -> text.
  const cm = globalThis.go6asm.assemble({
    source: ".org $0300\n\tLDA #1   ; the answer\n\tRTS\n",
  });
  if (!cm.comments || cm.comments["0x0300"] !== "the answer") {
    console.error("expected comment at 0x0300, got:", JSON.stringify(cm.comments));
    process.exit(1);
  }

  // Disassembly + static analysis (DESIGN §16).
  if (typeof lk.disasm !== "string" || !lk.disasm.includes("LDA")) {
    console.error("expected a disassembly string, got:", lk.disasm);
    process.exit(1);
  }
  if (!Array.isArray(lk.analysis)) {
    console.error("expected an analysis array, got:", lk.analysis);
    process.exit(1);
  }
  // A program that stores into ROM and falls into BRK must be flagged
  // (sim-tui target: ROM $E000-$FFFF read-only).
  const badMem = globalThis.go6asm.assemble({
    source: "\tSTA $E010\n\tBRK\n",
    target: "sim-tui",
  });
  const codes = (badMem.analysis || []).map((f) => f.code);
  if (!codes.includes("A0004") || !codes.includes("A0001")) {
    console.error("expected A0004 (write-ROM) + A0001 (BRK), got:",
      JSON.stringify(badMem.analysis));
    process.exit(1);
  }

  console.log("WASM SMOKE OK — bytes:", hex, "origin:", r.origin.toString(16),
    "| link:", lhex, "obj:", lk.object.length + "B",
    "| comment:", JSON.stringify(cm.comments["0x0300"]),
    "| analysis:", badMem.analysis.length + " findings");
  process.exit(0);
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
