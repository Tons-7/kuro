package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"kuro/internal/certs"
)

// SetCerts gives the server its HTTPS certificates, for the pairing links and Settings.
func (s *Server) SetCerts(src *certs.Source) { s.certs = src }

// httpsNames are the hosts a phone can open over HTTPS; empty without a certificate.
func (s *Server) httpsNames() []string {
	if s.certs == nil {
		return nil
	}
	return s.certs.Names()
}

// dualListener serves HTTP and HTTPS on one port, split by the first byte, so http://localhost keeps working.
type dualListener struct {
	net.Listener
	cfg   *tls.Config
	conns chan net.Conn
	errs  chan error
	done  chan struct{}
	once  sync.Once
}

// DualListener wraps ln; plain HTTP passes through, a TLS hello is terminated with cfg.
func DualListener(ln net.Listener, cfg *tls.Config) net.Listener {
	d := &dualListener{
		Listener: ln, cfg: cfg,
		conns: make(chan net.Conn), errs: make(chan error, 1), done: make(chan struct{}),
	}
	go d.accept()
	return d
}

func (d *dualListener) accept() {
	for {
		conn, err := d.Listener.Accept()
		if err != nil {
			d.errs <- err
			return
		}
		// Sniffed off the accept loop: a client that connects and says nothing holds up no one.
		go d.sniff(conn)
	}
}

const tlsHandshake = 0x16

func (d *dualListener) sniff(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	first, err := r.Peek(1)
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})

	var out net.Conn = &peeked{Conn: conn, r: r}
	if first[0] == tlsHandshake {
		out = tls.Server(out, d.cfg)
	}
	select {
	case d.conns <- out:
	case <-d.done:
		conn.Close()
	}
}

func (d *dualListener) Accept() (net.Conn, error) {
	select {
	case conn := <-d.conns:
		return conn, nil
	case err := <-d.errs:
		return nil, err
	case <-d.done:
		return nil, net.ErrClosed
	}
}

func (d *dualListener) Close() error {
	d.once.Do(func() { close(d.done) })
	return d.Listener.Close()
}

// peeked replays the sniffed byte to whoever reads the connection next.
type peeked struct {
	net.Conn
	r *bufio.Reader
}

func (p *peeked) Read(b []byte) (int, error) { return p.r.Read(b) }

const (
	duckDomainSetting = "https.duckdns.domain"
	duckTokenSetting  = "https.duckdns.token"
)

// StartDuckDNS resumes the automatic certificate the owner set up, if any.
func (s *Server) StartDuckDNS(ctx context.Context) {
	if s.certs == nil {
		return
	}
	domain, _ := s.store.Setting(ctx, duckDomainSetting)
	token, _ := s.store.Setting(ctx, duckTokenSetting)
	if domain == "" || token == "" {
		return
	}
	if err := s.certs.UseDuckDNS(domain, token); err != nil {
		s.log.Warn("duckdns", "err", err)
	}
}

func (s *Server) accessHTTPS(w http.ResponseWriter, r *http.Request) {
	if s.certs == nil {
		send(w, http.StatusOK, certs.Status{State: certs.StateOff})
		return
	}
	send(w, http.StatusOK, s.certs.Status())
}

// setAccessHTTPS saves the DuckDNS name and token (never sent back) and starts; an empty name turns it off.
func (s *Server) setAccessHTTPS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain string `json:"domain"`
		Token  string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || s.certs == nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "domain and token are needed"})
		return
	}
	if strings.TrimSpace(body.Domain) == "" {
		s.certs.StopDuckDNS()
		if err := s.store.SetSettings(r.Context(), map[string]string{duckDomainSetting: "", duckTokenSetting: ""}); err != nil {
			s.fail(w, "https off", err)
			return
		}
		send(w, http.StatusOK, s.certs.Status())
		return
	}
	// A blank token keeps the saved one, so the name can be corrected without pasting it again.
	token := strings.TrimSpace(body.Token)
	if token == "" {
		token, _ = s.store.Setting(r.Context(), duckTokenSetting)
	}
	if err := s.certs.UseDuckDNS(body.Domain, token); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	domain, _ := certs.DuckDomain(body.Domain)
	if err := s.store.SetSettings(r.Context(), map[string]string{duckDomainSetting: domain, duckTokenSetting: token}); err != nil {
		s.fail(w, "save https", err)
		return
	}
	send(w, http.StatusOK, s.certs.Status())
}
