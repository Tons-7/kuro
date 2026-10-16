package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A second save replaces the running DuckDNS manager and stops it; none is left pushing the address for good.
func TestSecondDuckDNSSetupStopsTheFirst(t *testing.T) {
	var calls []url.Values
	duckServer(t, "OK", &calls)
	s := New(t.TempDir(), func() string { return "" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.UseDuckDNS("mykuro", "token-1"); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	first := s.duck
	s.mu.RUnlock()
	if err := s.UseDuckDNS("mykuro", "token-2"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.StopDuckDNS)
	if first.ctx.Err() == nil {
		t.Fatal("the replaced manager is still running")
	}
	s.mu.RLock()
	current := s.duck
	s.mu.RUnlock()
	if current == first || current.token != "token-2" {
		t.Fatal("the new manager did not take over")
	}
}

// An owner certificate for this machine's own address pairs over https too.
func TestOwnCertificateForTheLANAddressNamesIt(t *testing.T) {
	ip := ownPrivateIP()
	if ip == nil {
		t.Skip("no private address on this machine")
	}
	cert, key := selfSignedFor(t, []string{"pc.local"}, []net.IP{ip, net.ParseIP("10.99.99.99")})
	s := New(t.TempDir(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.UseFiles(cert, key); err != nil {
		t.Fatal(err)
	}
	names := s.Names()
	if !slices.Contains(names, "pc.local") || !slices.Contains(names, ip.String()) {
		t.Fatalf("names = %v, want the DNS name and this machine's address", names)
	}
	if slices.Contains(names, "10.99.99.99") {
		t.Fatal("an address that is not this machine's was offered")
	}
}

func ownPrivateIP() net.IP {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.To4()
		}
	}
	return nil
}

func selfSignedFor(t *testing.T, dns []string, ips []net.IP) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: dns[0]},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: dns, IPAddresses: ips, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return certPath, keyPath
}
