package serve

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestCert creates a self-signed certificate and key and writes them
// to PEM files in the given directory. Returns the cert file path and key file
// path.
func generateTestCert(t *testing.T, dir, prefix string) (string, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	serial := big.NewInt(time.Now().UnixNano())
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: prefix + ".example.com",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	certFile := filepath.Join(dir, prefix+".pem")
	keyFile := filepath.Join(dir, prefix+"-key.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	require.NoError(t, os.WriteFile(certFile, certPEM, 0600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0600))

	return certFile, keyFile
}

// certSerial returns the serial number of the first certificate in the chain
// served by a certWatcher, or nil on error.
func certSerial(cw *certWatcher) *big.Int {
	cert, err := cw.getCertificate(nil)
	if err != nil || cert == nil {
		return nil
	}
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil
	}
	return x509Cert.SerialNumber
}

// TestCertWatcherInitialLoad verifies that a new certWatcher loads the
// certificate successfully and serves it via getCertificate.
func TestCertWatcherInitialLoad(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir, "initial")

	cw, err := newCertWatcher(certFile, keyFile)
	require.NoError(t, err)
	require.NotNil(t, cw)

	// getCertificate should return a non-nil cert.
	cert, err := cw.getCertificate(nil)
	require.NoError(t, err)
	assert.NotNil(t, cert)

	// The certificate should have at least one certificate in the chain.
	assert.NotEmpty(t, cert.Certificate)
	assert.NotNil(t, certSerial(cw))
}

// TestCertWatcherReload verifies that after writing a new certificate to disk,
// the watcher reloads and serves the updated certificate.
func TestCertWatcherReload(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir, "initial")

	cw, err := newCertWatcher(certFile, keyFile)
	require.NoError(t, err)
	cw.debounceDelay = 10 * time.Millisecond
	defer func() { _ = cw.Close() }()

	// Get the original cert's serial number.
	origSerial := certSerial(cw)
	require.NotNil(t, origSerial)

	// Start watching.
	cw.watch()

	// Give the watcher time to set up.
	time.Sleep(10 * time.Millisecond)

	// Write a new certificate pair to the same paths.
	generateTestCert(t, dir, "initial") // same prefix overwrites the files

	// Wait for debounce + reload (debounce is 10ms, give some extra time).
	time.Sleep(50 * time.Millisecond)

	// Get the new certificate.
	newSerial := certSerial(cw)
	require.NotNil(t, newSerial)

	assert.NotEqual(t, origSerial.String(), newSerial.String(),
		"certificate should have been reloaded with different serial")
}

// TestCertWatcherReloadFailureKeepsOldCert verifies that if reloading fails
// (e.g., only one file is updated), the old certificate is preserved.
func TestCertWatcherReloadFailureKeepsOldCert(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir, "initial")

	cw, err := newCertWatcher(certFile, keyFile)
	require.NoError(t, err)

	origSerial := certSerial(cw)
	require.NotNil(t, origSerial)

	// Corrupt the key file (write invalid PEM).
	require.NoError(t, os.WriteFile(keyFile, []byte("not a valid key"), 0600))

	// Manual reload attempt should fail.
	err = cw.reload()
	assert.Error(t, err, "reload should fail with invalid key material")

	// The old cert should still be served.
	newSerial := certSerial(cw)
	require.NotNil(t, newSerial)
	assert.Equal(t, origSerial.String(), newSerial.String(),
		"old certificate should still be served after failed reload")
}

// TestCertWatcherAtomicSave simulates an atomic save where files are renamed
// over the target paths.
func TestCertWatcherAtomicSave(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir, "initial")

	cw, err := newCertWatcher(certFile, keyFile)
	require.NoError(t, err)
	cw.debounceDelay = 10 * time.Millisecond
	defer func() { _ = cw.Close() }()

	origSerial := certSerial(cw)
	require.NotNil(t, origSerial)

	cw.watch()
	time.Sleep(10 * time.Millisecond)

	// Simulate atomic save: create temp files, then rename over originals.
	tmpDir := t.TempDir()
	newCertFile, newKeyFile := generateTestCert(t, tmpDir, "atomic")
	newCertData, err := os.ReadFile(newCertFile)
	require.NoError(t, err)
	newKeyData, err := os.ReadFile(newKeyFile)
	require.NoError(t, err)

	// Rename replaces the files (common pattern in editors and certbot).
	require.NoError(t, os.Rename(newCertFile, certFile))
	require.NoError(t, os.Rename(newKeyFile, keyFile))

	// After rename, the destination files contain the new data. But since
	// we're renaming from a different temp dir, the files moved correctly.
	// Verify they have content:
	certData, err := os.ReadFile(certFile)
	require.NoError(t, err)
	require.Equal(t, newCertData, certData)
	keyData, err := os.ReadFile(keyFile)
	require.NoError(t, err)
	require.Equal(t, newKeyData, keyData)

	// Wait for debounce (debounce is 10ms, give some extra time).
	time.Sleep(50 * time.Millisecond)

	newSerial := certSerial(cw)
	require.NotNil(t, newSerial)

	assert.NotEqual(t, origSerial.String(), newSerial.String(),
		"certificate should have been reloaded after atomic save")
}

// TestCertWatcherNilClientHello verifies getCertificate handles nil
// ClientHelloInfo (the common runtime case when TLS calls without SNI).
func TestCertWatcherNilClientHello(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir, "nil-hello")

	cw, err := newCertWatcher(certFile, keyFile)
	require.NoError(t, err)

	cert, err := cw.getCertificate(nil)
	require.NoError(t, err)
	assert.NotNil(t, cert)
}
