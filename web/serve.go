//go:build ignore

// serve.go is a tiny static file server for the web/ demo. It exists
// only because WebAssembly.instantiateStreaming() requires the wasm
// binary to be served as application/wasm — the stdlib FileServer (and
// python -m http.server on older Pythons) sends octet-stream and the
// streaming instantiate then rejects. Excluded from the build (//go:build
// ignore); run via `make serve`. Not part of the assembler.
package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"
	"strings"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	flag.Parse()

	root := http.Dir(".")
	fs := http.FileServer(root)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.ToLower(filepath.Ext(r.URL.Path)) {
		case ".wasm":
			w.Header().Set("Content-Type", "application/wasm")
		case ".js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		// No-store so a freshly rebuilt go6asm.wasm is always picked up.
		w.Header().Set("Cache-Control", "no-store")
		fs.ServeHTTP(w, r)
	})

	log.Printf("go6asm web demo: http://%s/  (Ctrl-C to stop)", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
