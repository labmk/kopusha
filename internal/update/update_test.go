package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
		why             string
	}{
		{"0.1.0", "0.2.0", true, "minor bump"},
		{"0.1.0", "0.1.1", true, "patch bump"},
		{"0.9.9", "1.0.0", true, "major bump"},
		{"0.1.0", "0.1.0", false, "same version is not an update"},
		{"0.2.0", "0.1.0", false, "older release must never prompt"},
		{"0.1.0", "v0.2.0", true, "tag keeps its v prefix"},
		{"1.2", "1.2.1", true, "longer version with equal prefix is newer"},
		{"1.2.1", "1.2", false, "shorter version with equal prefix is not"},
		// A version we cannot parse must stay silent rather than nag
		// forever with no way for the user to resolve it.
		{"0.1.0", "not-a-version", false, "unparseable latest"},
		{"dev", "0.2.0", false, "unparseable current"},
		{"0.1.0", "", false, "empty latest"},
		{"0.1.0", "0.1.-1", false, "negative component"},
		// 10 must beat 9, which string comparison would get wrong.
		{"0.9.0", "0.10.0", true, "numeric not lexical comparison"},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v — %s",
				c.current, c.latest, got, c.want, c.why)
		}
	}
}

func newTestChecker(t *testing.T, handler http.HandlerFunc) (*Checker, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("0.1.0", true)
	c.Endpoint = srv.URL
	return c, srv
}

func waitForCheck(t *testing.T, c *Checker) Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := c.Status(); s.Checked {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	return c.Status()
}

func TestStartReportsAvailableUpdate(t *testing.T) {
	c, _ := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.4.0","html_url":"https://example.com/r/0.4.0"}`))
	})
	c.Start(context.Background(), func(string, ...any) {})

	s := waitForCheck(t, c)
	if !s.Checked {
		t.Fatal("check never completed")
	}
	if !s.Available {
		t.Errorf("Available = false, want true (0.1.0 -> 0.4.0)")
	}
	if s.Latest != "0.4.0" {
		t.Errorf("Latest = %q, want 0.4.0 with the v stripped", s.Latest)
	}
	if s.URL != "https://example.com/r/0.4.0" {
		t.Errorf("URL = %q", s.URL)
	}
}

func TestStartIgnoresPrerelease(t *testing.T) {
	c, _ := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.9.9","prerelease":true}`))
	})
	c.Start(context.Background(), func(string, ...any) {})
	time.Sleep(200 * time.Millisecond)

	if s := c.Status(); s.Available || s.Checked {
		t.Errorf("prerelease must not be offered: %+v", s)
	}
}

// The check is on by default, so an unreachable endpoint is the normal
// case on an air-gapped host. It must not surface as an update state and
// must not block.
func TestStartSurvivesUnreachableEndpoint(t *testing.T) {
	c := New("0.1.0", true)
	c.Endpoint = "http://127.0.0.1:1/nope"
	c.Client = &http.Client{Timeout: 200 * time.Millisecond}

	logged := 0
	done := make(chan struct{})
	go func() {
		c.Start(context.Background(), func(string, ...any) { logged++ })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start blocked; it must return immediately")
	}

	time.Sleep(500 * time.Millisecond)
	s := c.Status()
	if s.Available || s.Checked {
		t.Errorf("failed check must leave status unknown, got %+v", s)
	}
	if s.Current != "0.1.0" || !s.Enabled {
		t.Errorf("status lost its baseline fields: %+v", s)
	}
}

func TestDisabledCheckerNeverCallsOut(t *testing.T) {
	called := false
	c, _ := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	})
	c.Enabled = false
	c.status.Enabled = false

	c.Start(context.Background(), func(string, ...any) {})
	time.Sleep(200 * time.Millisecond)

	if called {
		t.Error("disabled checker made a network request")
	}
	if s := c.Status(); s.Available || s.Checked || s.Enabled {
		t.Errorf("disabled checker reported state: %+v", s)
	}
}

func TestStartToleratesGarbage(t *testing.T) {
	for name, body := range map[string]string{
		"not json":  `<html>rate limited</html>`,
		"no tag":    `{"html_url":"https://example.com"}`,
		"empty":     ``,
		"wrong tag": `{"tag_name":"latest"}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			})
			c.Start(context.Background(), func(string, ...any) {})
			time.Sleep(150 * time.Millisecond)
			if s := c.Status(); s.Available {
				t.Errorf("garbage response produced an update offer: %+v", s)
			}
		})
	}
}

func TestFetchRejectsNon200(t *testing.T) {
	c, _ := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limit exceeded", http.StatusForbidden)
	})
	if _, _, err := c.fetch(context.Background()); err == nil {
		t.Error("want an error for HTTP 403, got nil")
	}
}

// Nothing about the host may be transmitted beyond what an HTTP GET
// unavoidably reveals.
func TestRequestCarriesNoHostInformation(t *testing.T) {
	var got http.Header
	var query string
	c, _ := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		query = r.URL.RawQuery
		w.Write([]byte(`{"tag_name":"v0.1.0"}`))
	})
	if _, _, err := c.fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if query != "" {
		t.Errorf("request carried a query string: %q", query)
	}
	if ua := got.Get("User-Agent"); ua != "kopusha/0.1.0" {
		t.Errorf("User-Agent = %q, want just the product and version", ua)
	}
	for _, banned := range []string{"Cookie", "Authorization", "X-Machine-Id"} {
		if got.Get(banned) != "" {
			t.Errorf("request carried %s", banned)
		}
	}
}
