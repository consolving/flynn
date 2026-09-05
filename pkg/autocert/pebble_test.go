//go:build !windows

package autocert

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestManagerObtainWithPebble exercises Obtain against a local Pebble ACME
// test server. It validates the HTTP-01 challenge path end-to-end and verifies
// the returned certificate chains back to the Pebble root CA.
func TestManagerObtainWithPebble(t *testing.T) {
	if os.Getenv("ACME_PEBBLE_TEST") == "" {
		t.Skip("set ACME_PEBBLE_TEST=1 to run Pebble integration test")
	}

	// Build a small HTTP-01 challenge server on any available port. Pebble's
	// validation authority will contact this port for the challenge token.
	challListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create challenge listener: %v", err)
	}
	defer challListener.Close()
	challengePort := challListener.Addr().(*net.TCPAddr).Port

	dirURL, rootPEM, cleanup := startPebble(t, challengePort)
	defer cleanup()

	// Configure lego to trust the Pebble root CA. The default HTTP client
	// picks up custom CAs from LEGO_CA_CERTIFICATES, and we also pass an
	// explicit client for tests.
	os.Setenv("LEGO_CA_CERTIFICATES", rootFileForEnv(rootPEM, t.TempDir()))
	defer os.Unsetenv("LEGO_CA_CERTIFICATES")

	rootPool := x509.NewCertPool()
	if !rootPool.AppendCertsFromPEM([]byte(rootPEM)) {
		t.Fatal("failed to parse Pebble root certificate")
	}
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: rootPool},
		},
	}

	cfg := DefaultConfig()
	cfg.Email = "test@example.com"
	cfg.CAURL = dirURL
	cfg.ChallengeType = ChallengeHTTP01
	cfg.TOSAgreed = true
	cfg.HTTPClient = httpClient

	store := newFakeStore()
	mgr := NewManager(cfg, store)

	// Start the challenge handler on the expected HTTP-01 port so Pebble's
	// validation authority can fetch the token from the manager's provider.
	challServer := &http.Server{Handler: mgr.HTTP01Handler()}
	go func() {
		if err := challServer.Serve(challListener); err != nil && err != http.ErrServerClosed {
			t.Logf("challenge server exited: %v", err)
		}
	}()
	defer challServer.Shutdown(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		cert, err := mgr.Obtain([]string{"localhost"})
		if err != nil {
			t.Errorf("Obtain failed: %v", err)
			return
		}
		if cert == nil {
			t.Error("Obtain returned nil certificate")
			return
		}
		if len(cert.Domains) != 1 || cert.Domains[0] != "localhost" {
			t.Errorf("unexpected certificate domains: %v", cert.Domains)
		}
		if err := verifyCertSignedByRoot(cert.CertPEM, rootPEM); err != nil {
			t.Errorf("certificate does not chain to Pebble root: %v", err)
		}
	}()

	// Wait a short time for lego to present the challenge before failing.
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for Obtain")
	}
}

func startPebble(t *testing.T, httpPort int) (dirURL, rootPEM string, cleanup func()) {
	rootFile := filepath.Join(t.TempDir(), "root.pem")

	pebbletestDir, err := filepath.Abs("pebbletest")
	if err != nil {
		t.Fatalf("failed to resolve pebbletest dir: %v", err)
	}

	// Ensure the pebbletest module builds with its own dependencies.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", ".",
		"-listen-addr", "127.0.0.1:0",
		"-http-port", fmt.Sprintf("%d", httpPort),
		"-root-cert-out", rootFile,
	)
	cmd.Dir = pebbletestDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to create stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start pebbletest: %v", err)
	}

	var dir string
	scanErr := make(chan error, 1)
	go func() {
		defer close(scanErr)
		scan := make(chan string, 10)
		go func() {
			buf := make([]byte, 1024)
			out := &bytes.Buffer{}
			for {
				n, err := stdout.Read(buf)
				if n > 0 {
					out.Write(buf[:n])
					for {
						line, rest, ok := bytes.Cut(out.Bytes(), []byte("\n"))
						if !ok {
							break
						}
						scan <- string(line)
						out.Reset()
						out.Write(rest)
					}
				}
				if err != nil {
					return
				}
			}
		}()

		timeout := time.After(30 * time.Second)
		for {
			select {
			case line, ok := <-scan:
				if !ok {
					scanErr <- fmt.Errorf("pebbletest stdout closed before PEBBLE_DIR")
					return
				}
				if strings.HasPrefix(line, "PEBBLE_DIR=") {
					dir = strings.TrimPrefix(line, "PEBBLE_DIR=")
					return
				}
			case <-timeout:
				scanErr <- fmt.Errorf("timed out waiting for PEBBLE_DIR")
				return
			}
		}
	}()

	go io.Copy(io.Discard, stderr)

	select {
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("timed out waiting for pebbletest PEBBLE_DIR")
	case err := <-scanErr:
		if err != nil {
			cmd.Process.Kill()
			t.Fatalf("failed to read Pebble directory URL: %v", err)
		}
	}

	// Wait until the directory endpoint responds.
	rootPool := x509.NewCertPool()
	if !rootPool.AppendCertsFromPEM([]byte(readFile(t, rootFile))) {
		t.Fatal("failed to parse Pebble root certificate")
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: rootPool, InsecureSkipVerify: true},
		},
	}
	if err := waitReady(client, dir); err != nil {
		cmd.Process.Kill()
		t.Fatalf("pebble directory not ready: %v", err)
	}

	rootData := readFile(t, rootFile)
	cleanup = func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
	return dir, rootData, cleanup
}

func waitReady(client *http.Client, url string) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("directory %s did not become ready", url)
}

func readFile(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(b)
}

func rootFileForEnv(rootPEM, dir string) string {
	path := filepath.Join(dir, "pebble-root.pem")
	if err := os.WriteFile(path, []byte(rootPEM), 0644); err != nil {
		panic(err)
	}
	return path
}

func verifyCertSignedByRoot(certPEM []byte, rootPEM string) error {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(rootPEM)) {
		return fmt.Errorf("failed to load root certificate")
	}
	intermediates := x509.NewCertPool()

	var cert *x509.Certificate
	for {
		block, rest := pem.Decode([]byte(certPEM))
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			certPEM = rest
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("failed to parse certificate: %w", err)
		}
		if cert == nil {
			cert = c
		} else {
			intermediates.AddCert(c)
		}
		certPEM = rest
	}
	if cert == nil {
		return fmt.Errorf("failed to decode certificate PEM")
	}

	opts := x509.VerifyOptions{
		DNSName:       "localhost",
		Roots:         roots,
		Intermediates: intermediates,
	}
	if _, err := cert.Verify(opts); err != nil {
		return err
	}
	return nil
}
