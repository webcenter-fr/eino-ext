// Command gen refreshes the committed libs/modelsdev/api.json snapshot from
// models.dev. It is invoked via `go generate` (see gen.go) or the
// models-dev-refresh Makefile target; it is not part of the library API.
package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	url := flag.String("url", "https://models.dev/api.json", "models.dev catalog URL")
	out := flag.String("out", "api.json", "output file path")
	timeout := flag.Duration("timeout", 30*time.Second, "request timeout")
	flag.Parse()

	client := &http.Client{Timeout: *timeout}
	resp, err := client.Get(*url)
	if err != nil {
		log.Fatalf("gen: fetching %s: %v", *url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("gen: unexpected status %d fetching %s", resp.StatusCode, *url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("gen: reading response body: %v", err)
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		log.Fatalf("gen: writing %s: %v", *out, err)
	}
	log.Printf("gen: wrote %d bytes to %s", len(body), *out)
}
