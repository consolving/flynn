// Command pebbletest starts a local Pebble ACME server for use by the
// pkg/autocert integration tests. It is intentionally a separate Go module so
// that Pebble's dependencies are not vendored into the main Flynn module.
//
//go:build !windows
package main

import (
	_ "embed"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jmhodges/clock"
	"github.com/letsencrypt/pebble/ca"
	"github.com/letsencrypt/pebble/db"
	"github.com/letsencrypt/pebble/va"
	"github.com/letsencrypt/pebble/wfe"
)

var (
	listenAddr  = flag.String("listen-addr", "localhost:0", "address the Pebble directory server listens on")
	httpPort    = flag.Int("http-port", 0, "port Pebble will validate HTTP-01 challenges against")
	rootCertOut = flag.String("root-cert-out", "", "path to write the trusted root CA certificates (PEM)")
)

//go:embed certs/localhost.pem
var tlsCert []byte

//go:embed certs/localhost-key.pem
var tlsKey []byte

//go:embed certs/root.pem
var minicaRoot []byte

func main() {
	flag.Parse()
	if *httpPort == 0 {
		fmt.Fprintln(os.Stderr, "-http-port is required")
		os.Exit(2)
	}

	logger := log.New(os.Stderr, "Pebble ", log.LstdFlags|log.Lmicroseconds)

	dir, err := os.MkdirTemp("", "pebbletest-*")
	if err != nil {
		logger.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, tlsCert, 0644); err != nil {
		logger.Fatalf("failed to write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, tlsKey, 0600); err != nil {
		logger.Fatalf("failed to write key: %v", err)
	}

	clk := clock.New()
	mdb := db.NewMemoryStore(clk)
	caImpl := ca.New(logger, mdb)
	vaImpl := va.New(logger, clk, *httpPort, 0, false)
	wfeImpl := wfe.New(logger, clk, mdb, vaImpl, caImpl, false)

	// Write the runtime Pebble root along with the minica root that signed the
	// directory server's TLS certificate. Tests need to trust both.
	if *rootCertOut != "" {
		rootPEM := append(minicaRoot, '\n')
		rootPEM = append(rootPEM, encodeCert(caImpl.GetRootCert().Cert.Raw)...)
		if err := os.WriteFile(*rootCertOut, rootPEM, 0644); err != nil {
			logger.Fatalf("failed to write root cert: %v", err)
		}
	}

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		logger.Fatalf("failed to listen on %s: %v", *listenAddr, err)
	}
	defer listener.Close()

	dirURL := fmt.Sprintf("https://%s%s", listener.Addr().String(), wfe.DirectoryPath)
	fmt.Println("PEBBLE_DIR=" + dirURL)
	if *rootCertOut != "" {
		fmt.Println("PEBBLE_ROOT=" + *rootCertOut)
	}
	os.Stdout.Sync()

	logger.Printf("ACME directory available at %s", dirURL)

	server := &http.Server{Handler: wfeImpl.Handler()}
	if err := server.ServeTLS(listener, certPath, keyPath); err != nil {
		logger.Fatalf("ServeTLS failed: %v", err)
	}
}

func encodeCert(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
