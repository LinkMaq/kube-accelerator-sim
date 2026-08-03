// Command kasim-ui-prototype serves three throwaway UI variants.
// It is not product code and never contacts Kubernetes.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"
)

//go:embed static/*
var prototypeAssets embed.FS

func main() {
	port := flag.Int("port", 18080, "loopback port")
	flag.Parse()

	assets, err := fs.Sub(prototypeAssets, "static")
	if err != nil {
		log.Fatal(err)
	}

	handler := http.FileServer(http.FS(assets))
	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", *port),
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			writer.Header().Set("Cache-Control", "no-store")
			handler.ServeHTTP(writer, request)
		}),
	}

	log.Printf("PROTOTYPE only: http://%s/?variant=A", server.Addr)
	log.Fatal(server.ListenAndServe())
}
