package cmd

import (
	"crypto/tls"
	"log/slog"
	"os"
	"sync"
	"time"
)

// certReloader dynamically reloads TLS certificates and private keys from disk
// when the underlying files are updated.
type certReloader struct {
	certFile string
	keyFile  string
	logger   *slog.Logger

	reloadMu sync.Mutex
	certMu   sync.RWMutex
	cert     *tls.Certificate

	certMod      time.Time
	keyMod       time.Time
	certSize     int64
	keySize      int64
	certLstatMod time.Time
	keyLstatMod  time.Time
}

// newCertReloader initializes a certReloader by performing an initial load of
// the certificate and private key from disk.
func newCertReloader(certFile, keyFile string, logger *slog.Logger) (*certReloader, error) {
	r := &certReloader{
		certFile: certFile,
		keyFile:  keyFile,
		logger:   logger,
	}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// reload reads and parses the certificate and private key files, updating the
// cached certificate and file metadata.
func (r *certReloader) reload() error {
	newCert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}

	certStat, err := os.Stat(r.certFile)
	if err != nil {
		return err
	}
	keyStat, err := os.Stat(r.keyFile)
	if err != nil {
		return err
	}

	certLstat, _ := os.Lstat(r.certFile)
	keyLstat, _ := os.Lstat(r.keyFile)

	r.certMu.Lock()
	r.cert = &newCert
	r.certMod = certStat.ModTime()
	r.keyMod = keyStat.ModTime()
	r.certSize = certStat.Size()
	r.keySize = keyStat.Size()
	if certLstat != nil {
		r.certLstatMod = certLstat.ModTime()
	}
	if keyLstat != nil {
		r.keyLstatMod = keyLstat.ModTime()
	}
	r.certMu.Unlock()

	return nil
}

// isChanged checks whether the cert or key files on disk have been modified
// compared to the cached metadata.
func (r *certReloader) isChanged(certStat, keyStat, certLstat, keyLstat os.FileInfo) bool {
	r.certMu.RLock()
	defer r.certMu.RUnlock()

	if r.cert == nil {
		return true
	}
	if !certStat.ModTime().Equal(r.certMod) || certStat.Size() != r.certSize {
		return true
	}
	if !keyStat.ModTime().Equal(r.keyMod) || keyStat.Size() != r.keySize {
		return true
	}
	if certLstat != nil && !certLstat.ModTime().Equal(r.certLstatMod) {
		return true
	}
	if keyLstat != nil && !keyLstat.ModTime().Equal(r.keyLstatMod) {
		return true
	}
	return false
}

// GetCertificate returns the active certificate, reloading it from disk if
// either the certificate or private key file has been updated.
func (r *certReloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	certStat, err := os.Stat(r.certFile)
	if err != nil {
		r.certMu.RLock()
		cached := r.cert
		r.certMu.RUnlock()
		if cached != nil {
			if r.logger != nil {
				r.logger.Warn("failed to stat TLS cert file, using cached certificate", "err", err)
			}
			return cached, nil
		}
		return nil, err
	}

	keyStat, err := os.Stat(r.keyFile)
	if err != nil {
		r.certMu.RLock()
		cached := r.cert
		r.certMu.RUnlock()
		if cached != nil {
			if r.logger != nil {
				r.logger.Warn("failed to stat TLS key file, using cached certificate", "err", err)
			}
			return cached, nil
		}
		return nil, err
	}

	certLstat, _ := os.Lstat(r.certFile)
	keyLstat, _ := os.Lstat(r.keyFile)

	if !r.isChanged(certStat, keyStat, certLstat, keyLstat) {
		r.certMu.RLock()
		cached := r.cert
		r.certMu.RUnlock()
		return cached, nil
	}

	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	// Double check under reloadMu in case another goroutine reloaded concurrently
	if !r.isChanged(certStat, keyStat, certLstat, keyLstat) {
		r.certMu.RLock()
		cached := r.cert
		r.certMu.RUnlock()
		return cached, nil
	}

	if err := r.reload(); err != nil {
		if r.logger != nil {
			r.logger.Warn("failed to reload TLS certificate, using cached certificate", "err", err)
		}
		r.certMu.RLock()
		cached := r.cert
		r.certMu.RUnlock()
		if cached != nil {
			return cached, nil
		}
		return nil, err
	}

	if r.logger != nil {
		r.logger.Info("reloaded TLS certificate", "cert_file", r.certFile, "key_file", r.keyFile)
	}

	r.certMu.RLock()
	cert := r.cert
	r.certMu.RUnlock()
	return cert, nil
}
