// Command sink is a target for the scale tests: an HTTP server that answers every request at
// once, in a process of its own, so the driver's measurements are the driver's.
//
//	go run ./internal/scale/sink -addr 127.0.0.1:18200
package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18200", "where to listen")
	flag.Parse()
	srv := &http.Server{Addr: *addr, ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
	})}
	log.Fatal(srv.ListenAndServe())
}
