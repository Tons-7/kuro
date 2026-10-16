// Package certs gets kuro an HTTPS certificate: the owner's files, or Let's Encrypt's for a DuckDNS name.
package certs

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Source answers TLS handshakes from whichever certificate covers the name asked for.
type Source struct {
	log *slog.Logger
	// dir keeps the Let's Encrypt account and certificates across restarts.
	dir string
	// lanIP is the address a DuckDNS name should point at.
	lanIP func() string

	mu        sync.RWMutex
	files     *certFiles
	fileNames []string
	duck      *duck
}

func New(dir string, lanIP func() string, log *slog.Logger) *Source {
	return &Source{log: log, dir: dir, lanIP: lanIP}
}

// TLSConfig serves the certificates this source holds, as they come and go.
func (s *Source) TLSConfig() *tls.Config {
	return &tls.Config{GetCertificate: s.certificate, MinVersion: tls.VersionTLS12}
}

var errNoCertificate = errors.New("no certificate for this name")

func (s *Source) certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.RLock()
	files, d := s.files, s.duck
	s.mu.RUnlock()
	if d != nil && strings.EqualFold(hello.ServerName, d.domain) {
		return d.config.GetCertificate(hello)
	}
	if files != nil {
		return files.get()
	}
	return nil, errNoCertificate
}

// Names are the hosts a phone can open over HTTPS right now, for the pairing links.
func (s *Source) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	if s.duck != nil && s.duck.ready() {
		out = append(out, s.duck.domain)
	}
	return append(out, s.fileNames...)
}

// UseFiles serves the owner's own certificate (tls_cert, tls_key).
func (s *Source) UseFiles(cert, key string) error {
	files := &certFiles{cert: cert, key: key}
	pair, err := files.get()
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	var names []string
	for _, n := range leaf.DNSNames {
		if !strings.Contains(n, "*") {
			names = append(names, n)
		}
	}
	// A certificate for this machine's own address (mkcert's usual kind) pairs over https too.
	for _, ip := range leaf.IPAddresses {
		if ip.IsPrivate() && hasAddr(ip) {
			names = append(names, ip.String())
		}
	}
	s.mu.Lock()
	s.files, s.fileNames = files, names
	s.mu.Unlock()
	return nil
}

// hasAddr reports an address of this machine.
func hasAddr(ip net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// certFiles serves a certificate from disk, reread when the file changes so a renewal needs no restart.
type certFiles struct {
	cert, key string

	mu     sync.Mutex
	loaded *tls.Certificate
	stamp  time.Time
}

func (c *certFiles) get() (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Stat(c.cert)
	if err == nil && c.loaded != nil && info.ModTime().Equal(c.stamp) {
		return c.loaded, nil
	}
	pair, err := tls.LoadX509KeyPair(c.cert, c.key)
	if err != nil {
		// A renewal half written: keep serving the one that worked.
		if c.loaded != nil {
			return c.loaded, nil
		}
		return nil, err
	}
	c.loaded = &pair
	if info != nil {
		c.stamp = info.ModTime()
	}
	return c.loaded, nil
}
