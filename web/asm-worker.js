// Web Worker that runs go6asm.wasm off the UI thread (DESIGN §10.3).
// The main thread posts assemble options; the worker posts back the
// result. The wasm binary has no filesystem/fetch: source comes in by
// string, bytes go out.
importScripts("wasm_exec.js");

let ready = (async () => {
  const go = new Go();
  const r = await WebAssembly.instantiateStreaming(fetch("go6asm.wasm"), go.importObject);
  go.run(r.instance); // sets self.go6asm, then blocks
  await new Promise((res) => setTimeout(res, 0));
})();

self.onmessage = async (e) => {
  await ready;
  const r = self.go6asm.assemble(e.data);
  // Copy the typed arrays out so they survive the structured clone.
  const msg = {
    success: r.success,
    bytes: Uint8Array.from(r.bytes),
    origin: r.origin,
    symbols: r.symbols,
    comments: r.comments,
    listing: r.listing,
    lbl: r.lbl,
    sym: r.sym,
    disasm: r.disasm,
    analysis: r.analysis,
    errors: r.errors,
    warnings: r.warnings,
  };
  if (r.object) msg.object = Uint8Array.from(r.object);
  self.postMessage(msg);
};
