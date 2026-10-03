package certs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/duckdns"
	"go.uber.org/zap"
)

const duckSuffix = ".duckdns.org"

// DuckUpdateURL is DuckDNS's one endpoint; a variable so tests can stand in for it.
var DuckUpdateURL = "https://www.duckdns.org/update"

// duck keeps one DuckDNS name pointed at this machine and certified.
type duck struct {
	domain, token string
	config        *certmagic.Config
	cache         *certmagic.Cache
	stop          context.CancelFunc

	mu      sync.Mutex
	state   string // working | ready | error
	problem string
	ip      string
}

// ready is whether a certificate is loaded, however it got there: fetched just now or read from disk.
func (d *duck) ready() bool {
	for _, cert := range d.cache.AllMatchingCertificates(d.domain) {
		if cert.Leaf != nil && time.Now().Before(cert.Leaf.NotAfter) {
			return true
		}
	}
	return false
}

func (d *duck) set(state, problem string) {
	d.mu.Lock()
	d.state, d.problem = state, problem
	d.mu.Unlock()
}

const (
	StateOff     = "off"
	StateWorking = "working"
	StateReady   = "ready"
	StateError   = "error"
)

// Status is what Settings shows about the automatic certificate.
type Status struct {
	Domain  string `json:"domain"`
	State   string `json:"state"`
	Problem string `json:"problem,omitempty"`
	// Expires is when the certificate runs out, unix seconds; renewed well before.
	Expires int64  `json:"expires,omitempty"`
	IP      string `json:"ip,omitempty"`
}

func (s *Source) Status() Status {
	s.mu.RLock()
	d := s.duck
	s.mu.RUnlock()
	if d == nil {
		return Status{State: StateOff}
	}
	d.mu.Lock()
	out := Status{Domain: d.domain, State: d.state, Problem: d.problem, IP: d.ip}
	d.mu.Unlock()
	for _, cert := range d.cache.AllMatchingCertificates(d.domain) {
		if cert.Leaf != nil {
			out.Expires = cert.Leaf.NotAfter.Unix()
		}
	}
	// A loaded certificate is ready, whatever the last attempt said.
	if d.ready() {
		out.State, out.Problem = StateReady, ""
	}
	return out
}

var duckName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// DuckDomain turns what a person types ("mykuro", "MyKuro.duckdns.org", a pasted URL) into the full name.
func DuckDomain(typed string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(typed))
	name = strings.TrimPrefix(strings.TrimPrefix(name, "https://"), "http://")
	name, _, _ = strings.Cut(name, "/")
	name, _, _ = strings.Cut(name, ":")
	name = strings.TrimSuffix(name, duckSuffix)
	if !duckName.MatchString(name) {
		return "", errors.New("the name is the part before .duckdns.org: letters, digits and dashes")
	}
	return name + duckSuffix, nil
}

// UseDuckDNS points the name here and keeps a Let's Encrypt certificate for it; Status reports progress.
func (s *Source) UseDuckDNS(typed, token string) error {
	domain, err := DuckDomain(typed)
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("the token from duckdns.org is needed")
	}
	s.StopDuckDNS()

	ctx, cancel := context.WithCancel(context.Background())
	d := &duck{domain: domain, token: token, stop: cancel, state: StateWorking}
	d.cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return d.config, nil },
		Logger:           zap.NewNop(),
	})
	d.config = certmagic.New(d.cache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: s.dir},
		Logger:  zap.NewNop(),
		// Issuance runs in the library's background mode, which reports only through events.
		OnEvent: func(_ context.Context, event string, data map[string]any) error {
			switch event {
			case "cert_obtained":
				d.set(StateReady, "")
				s.log.Info("https certificate ready", "name", domain)
			case "cert_failed":
				err, _ := data["error"].(error)
				if err == nil {
					err = errors.New("the certificate authority refused")
				}
				// A failed renewal leaves the current certificate serving.
				if renewal, _ := data["renewal"].(bool); !renewal {
					d.set(StateError, plainProblem(err, token))
				}
				s.log.Warn("https certificate", "name", domain, "err", plainProblem(err, token))
			}
			return nil
		},
	})
	ca := certmagic.LetsEncryptProductionCA
	// Let's Encrypt's staging CA, for trying the flow without spending real issuance limits.
	if os.Getenv("KURO_ACME_STAGING") == "1" {
		ca = certmagic.LetsEncryptStagingCA
	}
	d.config.Issuers = []certmagic.Issuer{certmagic.NewACMEIssuer(d.config, certmagic.ACMEIssuer{
		CA: ca, Agreed: true, Logger: zap.NewNop(),
		// The name points at a private address, so only the DNS challenge can prove it.
		DisableHTTPChallenge: true, DisableTLSALPNChallenge: true,
		DNS01Solver: &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{
			DNSProvider: &duckdns.Provider{APIToken: token},
			Logger:      zap.NewNop(),
		}},
	})}

	s.mu.Lock()
	s.duck = d
	s.mu.Unlock()
	go s.runDuck(ctx, d)
	return nil
}

// StopDuckDNS turns the automatic certificate off; the files on disk stay for next time.
func (s *Source) StopDuckDNS() {
	s.mu.Lock()
	d := s.duck
	s.duck = nil
	s.mu.Unlock()
	if d != nil {
		d.stop()
		d.cache.Stop()
	}
}

const (
	duckRetry   = 10 * time.Minute
	duckIPCheck = 5 * time.Minute
)

func (s *Source) runDuck(ctx context.Context, d *duck) {
	// Until it succeeds: a wrong token or no internet is fixed by the owner, not by giving up.
	for {
		err := s.pointDuck(ctx, d)
		if err == nil {
			// Async: never prompts on the terminal; a new certificate arrives through OnEvent.
			err = d.config.ManageAsync(ctx, []string{d.domain})
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if len(d.cache.AllMatchingCertificates(d.domain)) > 0 {
				d.set(StateReady, "")
				s.log.Info("https certificate ready", "name", d.domain)
			}
			break
		}
		d.set(StateError, plainProblem(err, d.token))
		s.log.Warn("https certificate", "name", d.domain, "err", plainProblem(err, d.token))
		select {
		case <-ctx.Done():
			return
		case <-time.After(duckRetry):
			d.set(StateWorking, "")
		}
	}

	tick := time.NewTicker(duckIPCheck)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := s.pointDuck(ctx, d); err != nil {
				s.log.Warn("duckdns address update", "name", d.domain, "err", err)
			}
		}
	}
}

// pointDuck sets the name's address to this machine's, when it is not already.
func (s *Source) pointDuck(ctx context.Context, d *duck) error {
	ip := s.lanIP()
	if ip == "" {
		return errors.New("this machine has no network address right now")
	}
	d.mu.Lock()
	same := d.ip == ip
	d.mu.Unlock()
	if same {
		return nil
	}

	q := url.Values{"domains": {strings.TrimSuffix(d.domain, duckSuffix)}, "token": {d.token}, "ip": {ip}}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DuckUpdateURL+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach duckdns.org: %w", urlless(err))
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64))
	if !strings.HasPrefix(string(body), "OK") {
		return errors.New("duckdns.org refused the name or token; check both on duckdns.org")
	}
	d.mu.Lock()
	d.ip = ip
	d.mu.Unlock()
	return nil
}

// urlless drops the request URL from an error: it carries the token.
func urlless(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}

// plainProblem is the error as Settings shows it: one line, no token.
func plainProblem(err error, token string) string {
	msg := strings.ReplaceAll(urlless(err).Error(), token, "…")
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}
