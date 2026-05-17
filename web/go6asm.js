// Tiny host helper: wraps the assembler Web Worker in a promise API.
//
//   import { Assembler } from "./go6asm.js";
//   const asm = new Assembler();
//   const r = await asm.assemble({ source, origin: 0x0600 });
//
// One request at a time (the assembler is fast and synchronous in wasm);
// callers that need concurrency can create multiple Assemblers.
export class Assembler {
  constructor(workerURL = "asm-worker.js") {
    this.worker = new Worker(workerURL);
    this.pending = null;
    this.worker.onmessage = (e) => {
      const p = this.pending;
      this.pending = null;
      if (p) p.resolve(e.data);
    };
    this.worker.onerror = (e) => {
      const p = this.pending;
      this.pending = null;
      if (p) p.reject(e);
    };
  }

  assemble(opts) {
    if (this.pending) return Promise.reject(new Error("assemble already in flight"));
    return new Promise((resolve, reject) => {
      this.pending = { resolve, reject };
      this.worker.postMessage(opts);
    });
  }

  terminate() {
    this.worker.terminate();
  }
}
