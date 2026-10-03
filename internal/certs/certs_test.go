package certs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDuckDomainAcceptsWhatPeopleType(t *testing.T) {
	for typed, want := range map[string]string{
		"mykuro":                          "mykuro.duckdns.org",
		" MyKuro.duckdns.org ":            "mykuro.duckdns.org",
		"https://mykuro.duckdns.org:4321": "mykuro.duckdns.org",
		"http://my-kuro.duckdns.org/":     "my-kuro.duckdns.org",
	} {
		got, err := DuckDomain(typed)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", typed, got, err, want)
		}
	}
	for _, typed := range []string{"", "my kuro", "a.b", "-kuro", "kuro.example.com"} {
		if got, err := DuckDomain(typed); err == nil {
			t.Errorf("%q accepted as %q", typed, got)
		}
	}
}

func duckServer(t *testing.T, answer string, calls *[]url.Values) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.Query())
		io.WriteString(w, answer)
	}))
	old := DuckUpdateURL
	DuckUpdateURL = srv.URL
	t.Cleanup(func() { DuckUpdateURL = old; srv.Close() })
}

func TestPointDuckSendsTheLANAddressOncePerChange(t *testing.T) {
	var calls []url.Values
	duckServer(t, "OK", &calls)
	ip := "192.168.1.20"
	s := New(t.TempDir(), func() string { return ip }, slog.New(slog.DiscardHandler))
	d := &duck{domain: "mykuro.duckdns.org", token: "tok"}

	for range 2 {
		if err := s.pointDuck(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("%d updates for an unchanged address, want 1", len(calls))
	}
	if q := calls[0]; q.Get("domains") != "mykuro" || q.Get("token") != "tok" || q.Get("ip") != ip {
		t.Fatalf("sent %v", q)
	}

	ip = "192.168.1.77"
	if err := s.pointDuck(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1].Get("ip") != ip {
		t.Fatalf("a new address was not sent: %v", calls)
	}
}

func TestPointDuckReportsARefusal(t *testing.T) {
	var calls []url.Values
	duckServer(t, "KO", &calls)
	s := New(t.TempDir(), func() string { return "192.168.1.20" }, slog.New(slog.DiscardHandler))
	d := &duck{domain: "mykuro.duckdns.org", token: "wrong"}
	if err := s.pointDuck(context.Background(), d); err == nil {
		t.Fatal("a refused token was accepted")
	}
	if d.ip != "" {
		t.Error("the address was remembered despite the refusal")
	}
}

func TestProblemsNeverCarryTheToken(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://www.duckdns.org/update?token=s3cret", Err: errors.New("dial failed for s3cret\nsecond line")}
	got := plainProblem(err, "s3cret")
	if strings.Contains(got, "s3cret") || strings.Contains(got, "\n") {
		t.Fatalf("problem = %q", got)
	}
}

func TestStatusIsOffUntilSetUp(t *testing.T) {
	s := New(t.TempDir(), func() string { return "" }, slog.New(slog.DiscardHandler))
	if st := s.Status(); st.State != StateOff {
		t.Fatalf("state = %q", st.State)
	}
	if err := s.UseDuckDNS("mykuro", ""); err == nil {
		t.Fatal("accepted without a token")
	}
	if names := s.Names(); len(names) != 0 {
		t.Fatalf("names = %v", names)
	}
}
