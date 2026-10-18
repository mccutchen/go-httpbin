package cmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

func generateTestCert(t *testing.T, commonName string) ([]byte, []byte) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %s", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("failed to generate serial number: %s", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost", commonName},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %s", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal private key: %s", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	return certPEM, keyPEM
}

func getCertCommonName(t *testing.T, cert *tls.Certificate) string {
	t.Helper()
	if len(cert.Certificate) == 0 {
		t.Fatal("empty certificate chain")
	}
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse x509 certificate: %s", err)
	}
	return x509Cert.Subject.CommonName
}

func TestCertReloader_InitialLoad(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	certFile := filepath.Join(tempDir, "server.crt")
	keyFile := filepath.Join(tempDir, "server.key")

	certPEM, keyPEM := generateTestCert(t, "initial-test")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("failed to write cert file: %s", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("failed to write key file: %s", err)
	}

	// Happy path
	reloader, err := newCertReloader(certFile, keyFile, nil)
	assert.NilError(t, err)

	cert, err := reloader.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, getCertCommonName(t, cert), "initial-test", "certificate common name mismatch")

	// Missing cert file
	_, err = newCertReloader(filepath.Join(tempDir, "nonexistent.crt"), keyFile, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent cert file, got nil")
	}

	// Missing key file
	_, err = newCertReloader(certFile, filepath.Join(tempDir, "nonexistent.key"), nil)
	if err == nil {
		t.Fatal("expected error for nonexistent key file, got nil")
	}

	// Corrupted cert file
	corruptedCertFile := filepath.Join(tempDir, "corrupted.crt")
	if err := os.WriteFile(corruptedCertFile, []byte("bad-pem-data"), 0o600); err != nil {
		t.Fatalf("failed to write corrupted cert: %s", err)
	}
	_, err = newCertReloader(corruptedCertFile, keyFile, nil)
	if err == nil {
		t.Fatal("expected error for corrupted cert file, got nil")
	}

	// Mismatched private key
	_, otherKeyPEM := generateTestCert(t, "other")
	mismatchedKeyFile := filepath.Join(tempDir, "mismatched.key")
	if err := os.WriteFile(mismatchedKeyFile, otherKeyPEM, 0o600); err != nil {
		t.Fatalf("failed to write mismatched key: %s", err)
	}
	_, err = newCertReloader(certFile, mismatchedKeyFile, nil)
	if err == nil {
		t.Fatal("expected error for mismatched key, got nil")
	}
}

func TestCertReloader_ReloadOnUpdate(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	certFile := filepath.Join(tempDir, "server.crt")
	keyFile := filepath.Join(tempDir, "server.key")

	certPEM1, keyPEM1 := generateTestCert(t, "cert-v1")
	if err := os.WriteFile(certFile, certPEM1, 0o600); err != nil {
		t.Fatalf("failed to write cert1: %s", err)
	}
	if err := os.WriteFile(keyFile, keyPEM1, 0o600); err != nil {
		t.Fatalf("failed to write key1: %s", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reloader, err := newCertReloader(certFile, keyFile, logger)
	assert.NilError(t, err)

	cert1, err := reloader.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, getCertCommonName(t, cert1), "cert-v1", "expected cert-v1")

	// Sleep briefly to ensure filesystem modification time differs
	time.Sleep(20 * time.Millisecond)

	// Write updated certificate and key
	certPEM2, keyPEM2 := generateTestCert(t, "cert-v2")
	if err := os.WriteFile(certFile, certPEM2, 0o600); err != nil {
		t.Fatalf("failed to write cert2: %s", err)
	}
	if err := os.WriteFile(keyFile, keyPEM2, 0o600); err != nil {
		t.Fatalf("failed to write key2: %s", err)
	}

	cert2, err := reloader.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, getCertCommonName(t, cert2), "cert-v2", "expected cert-v2 after file update")
}

func TestCertReloader_FallbackOnError(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	certFile := filepath.Join(tempDir, "server.crt")
	keyFile := filepath.Join(tempDir, "server.key")

	certPEM1, keyPEM1 := generateTestCert(t, "cert-valid")
	if err := os.WriteFile(certFile, certPEM1, 0o600); err != nil {
		t.Fatalf("failed to write cert1: %s", err)
	}
	if err := os.WriteFile(keyFile, keyPEM1, 0o600); err != nil {
		t.Fatalf("failed to write key1: %s", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reloader, err := newCertReloader(certFile, keyFile, logger)
	assert.NilError(t, err)

	cert, err := reloader.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, getCertCommonName(t, cert), "cert-valid", "expected cert-valid")

	time.Sleep(20 * time.Millisecond)

	// Overwrite cert file with corrupt data
	if err := os.WriteFile(certFile, []byte("invalid-certificate-bytes"), 0o600); err != nil {
		t.Fatalf("failed to write corrupted cert: %s", err)
	}

	// Reloader should keep serving the valid cached certificate instead of returning error
	certFallback, err := reloader.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, getCertCommonName(t, certFallback), "cert-valid", "expected fallback to cached valid cert")

	time.Sleep(20 * time.Millisecond)

	// Overwrite with new valid certificate
	certPEM2, keyPEM2 := generateTestCert(t, "cert-recovered")
	if err := os.WriteFile(certFile, certPEM2, 0o600); err != nil {
		t.Fatalf("failed to write cert2: %s", err)
	}
	if err := os.WriteFile(keyFile, keyPEM2, 0o600); err != nil {
		t.Fatalf("failed to write key2: %s", err)
	}

	certRecovered, err := reloader.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, getCertCommonName(t, certRecovered), "cert-recovered", "expected reload of recovered cert")
}

func TestCertReloader_Symlinks(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	v1Dir := filepath.Join(tempDir, "v1")
	v2Dir := filepath.Join(tempDir, "v2")
	if err := os.Mkdir(v1Dir, 0o755); err != nil {
		t.Fatalf("failed to create v1 dir: %s", err)
	}
	if err := os.Mkdir(v2Dir, 0o755); err != nil {
		t.Fatalf("failed to create v2 dir: %s", err)
	}

	certPEM1, keyPEM1 := generateTestCert(t, "symlink-v1")
	if err := os.WriteFile(filepath.Join(v1Dir, "server.crt"), certPEM1, 0o600); err != nil {
		t.Fatalf("failed to write v1 cert: %s", err)
	}
	if err := os.WriteFile(filepath.Join(v1Dir, "server.key"), keyPEM1, 0o600); err != nil {
		t.Fatalf("failed to write v1 key: %s", err)
	}

	certPEM2, keyPEM2 := generateTestCert(t, "symlink-v2")
	if err := os.WriteFile(filepath.Join(v2Dir, "server.crt"), certPEM2, 0o600); err != nil {
		t.Fatalf("failed to write v2 cert: %s", err)
	}
	if err := os.WriteFile(filepath.Join(v2Dir, "server.key"), keyPEM2, 0o600); err != nil {
		t.Fatalf("failed to write v2 key: %s", err)
	}

	// Create symlink data -> v1
	dataSymlink := filepath.Join(tempDir, "data")
	if err := os.Symlink(v1Dir, dataSymlink); err != nil {
		t.Fatalf("failed to create data symlink: %s", err)
	}

	certFile := filepath.Join(tempDir, "server.crt")
	keyFile := filepath.Join(tempDir, "server.key")
	if err := os.Symlink(filepath.Join(dataSymlink, "server.crt"), certFile); err != nil {
		t.Fatalf("failed to create cert symlink: %s", err)
	}
	if err := os.Symlink(filepath.Join(dataSymlink, "server.key"), keyFile); err != nil {
		t.Fatalf("failed to create key symlink: %s", err)
	}

	reloader, err := newCertReloader(certFile, keyFile, nil)
	assert.NilError(t, err)

	cert1, err := reloader.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, getCertCommonName(t, cert1), "symlink-v1", "expected symlink-v1")

	time.Sleep(20 * time.Millisecond)

	// Atomically switch data symlink -> v2 (typical Kubernetes secret rotation pattern)
	dataTmpSymlink := filepath.Join(tempDir, "data_tmp")
	if err := os.Symlink(v2Dir, dataTmpSymlink); err != nil {
		t.Fatalf("failed to create data_tmp symlink: %s", err)
	}
	if err := os.Rename(dataTmpSymlink, dataSymlink); err != nil {
		t.Fatalf("failed to rename symlink: %s", err)
	}

	cert2, err := reloader.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, getCertCommonName(t, cert2), "symlink-v2", "expected symlink-v2 after symlink swap")
}

func TestCertReloader_Concurrency(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	certFile := filepath.Join(tempDir, "server.crt")
	keyFile := filepath.Join(tempDir, "server.key")

	certPEM, keyPEM := generateTestCert(t, "concurrent-0")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("failed to write cert: %s", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("failed to write key: %s", err)
	}

	reloader, err := newCertReloader(certFile, keyFile, nil)
	assert.NilError(t, err)

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Readers calling GetCertificate concurrently
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					cert, err := reloader.GetCertificate(nil)
					if err != nil {
						t.Errorf("unexpected error in concurrent reader: %s", err)
						return
					}
					if cert == nil {
						t.Error("expected non-nil certificate")
						return
					}
				}
			}
		}()
	}

	// Writer updating certificates
	for i := 1; i <= 5; i++ {
		time.Sleep(10 * time.Millisecond)
		newCertPEM, newKeyPEM := generateTestCert(t, fmt.Sprintf("concurrent-%d", i))
		if err := os.WriteFile(certFile, newCertPEM, 0o600); err != nil {
			t.Fatalf("failed to update cert: %s", err)
		}
		if err := os.WriteFile(keyFile, newKeyPEM, 0o600); err != nil {
			t.Fatalf("failed to update key: %s", err)
		}
	}

	close(stopCh)
	wg.Wait()
}

func TestCertReloader_LiveHTTPS(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	certFile := filepath.Join(tempDir, "server.crt")
	keyFile := filepath.Join(tempDir, "server.key")

	certPEM1, keyPEM1 := generateTestCert(t, "live-cert-1")
	if err := os.WriteFile(certFile, certPEM1, 0o600); err != nil {
		t.Fatalf("failed to write cert1: %s", err)
	}
	if err := os.WriteFile(keyFile, keyPEM1, 0o600); err != nil {
		t.Fatalf("failed to write key1: %s", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reloader, err := newCertReloader(certFile, keyFile, logger)
	assert.NilError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %s", err)
	}
	defer ln.Close()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}),
		TLSConfig: &tls.Config{
			GetCertificate: reloader.GetCertificate,
		},
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- srv.ServeTLS(ln, "", "")
	}()
	defer func() {
		_ = srv.Close()
		<-serverDone
	}()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			DisableKeepAlives: true,
		},
	}

	url := fmt.Sprintf("https://%s/", ln.Addr().String())

	// First request: should receive live-cert-1
	resp1, err := client.Get(url)
	assert.NilError(t, err)
	defer resp1.Body.Close()
	assert.Equal(t, resp1.StatusCode, http.StatusOK, "unexpected status code")
	assert.Equal(t, resp1.TLS.PeerCertificates[0].Subject.CommonName, "live-cert-1", "expected live-cert-1")

	time.Sleep(20 * time.Millisecond)

	// Update cert and key files
	certPEM2, keyPEM2 := generateTestCert(t, "live-cert-2")
	if err := os.WriteFile(certFile, certPEM2, 0o600); err != nil {
		t.Fatalf("failed to write cert2: %s", err)
	}
	if err := os.WriteFile(keyFile, keyPEM2, 0o600); err != nil {
		t.Fatalf("failed to write key2: %s", err)
	}

	// Second request: should receive live-cert-2
	resp2, err := client.Get(url)
	assert.NilError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, resp2.StatusCode, http.StatusOK, "unexpected status code")
	assert.Equal(t, resp2.TLS.PeerCertificates[0].Subject.CommonName, "live-cert-2", "expected live-cert-2 after update")
}
