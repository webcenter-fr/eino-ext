package egress

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestProxy(t *testing.T, pol *Policy, lookup func(string) ([]net.IP, error)) (*Proxy, string) {
	t.Helper()

	proxy, err := NewProxy(context.Background(), pol)
	require.NoError(t, err)
	if lookup != nil {
		proxy.lookupIP = lookup
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = proxy.Serve(ctx, ln)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("proxy did not stop")
		}
	})

	require.Eventually(t, func() bool { return proxy.Addr() != "" }, time.Second, 10*time.Millisecond)
	return proxy, proxy.Addr()
}

func staticLookup(mapping map[string]string) func(string) ([]net.IP, error) {
	return func(host string) ([]net.IP, error) {
		ip, ok := mapping[host]
		if !ok {
			return nil, fmt.Errorf("no such host %q", host)
		}
		return []net.IP{net.ParseIP(ip)}, nil
	}
}

func TestProxy_ServeHTTP_CONNECT(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	require.NoError(t, err)
	target := originURL.Host

	pol := &Policy{DefaultDeny: true, AllowLocalNetwork: true, AllowHosts: []string{"allowed.test"}}
	_, proxyAddr := newTestProxy(t, pol, staticLookup(map[string]string{
		"allowed.test": "127.0.0.1",
		"denied.test":  "203.0.113.1",
	}))

	t.Run("allowed host tunnels", func(t *testing.T) {
		conn, err := net.Dial("tcp", proxyAddr)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()

		_, err = fmt.Fprintf(conn, "CONNECT allowed.test:%s HTTP/1.1\r\nHost: allowed.test:%s\r\n\r\n", originURL.Port(), originURL.Port())
		require.NoError(t, err)

		br := bufio.NewReader(conn)
		status, err := br.ReadString('\n')
		require.NoError(t, err)
		assert.Contains(t, status, "200")

		for {
			line, err := br.ReadString('\n')
			require.NoError(t, err)
			if line == "\r\n" {
				break
			}
		}

		_, err = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target)
		require.NoError(t, err)

		resp, err := http.ReadResponse(br, nil)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "ok", string(body))
	})

	t.Run("denied host is forbidden, not 404", func(t *testing.T) {
		conn, err := net.Dial("tcp", proxyAddr)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()

		_, err = fmt.Fprintf(conn, "CONNECT denied.test:443 HTTP/1.1\r\nHost: denied.test:443\r\n\r\n")
		require.NoError(t, err)

		status, err := bufio.NewReader(conn).ReadString('\n')
		require.NoError(t, err)
		assert.Contains(t, status, "403")
		assert.NotContains(t, status, "404")
	})
}

func TestProxy_ServeHTTP_PlainHTTP(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	require.NoError(t, err)

	pol := &Policy{DefaultDeny: true, AllowLocalNetwork: true, AllowHosts: []string{"allowed.test"}}
	proxy, proxyAddr := newTestProxy(t, pol, staticLookup(map[string]string{
		"allowed.test": "127.0.0.1",
		"denied.test":  "203.0.113.1",
	}))

	proxyURL, err := url.Parse(fmt.Sprintf("http://%s", proxyAddr))
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	t.Run("absolute-form allowed is relayed", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("http://allowed.test:%s/", originURL.Port()))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "ok", string(body))
	})

	t.Run("absolute-form denied is forbidden", func(t *testing.T) {
		resp, err := client.Get("http://denied.test/")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("relative-form is rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		proxy.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "absolute-form")
	})
}

func TestProxy_DNSFailure(t *testing.T) {
	pol := &Policy{DefaultDeny: true}
	_, proxyAddr := newTestProxy(t, pol, staticLookup(map[string]string{}))

	proxyURL, err := url.Parse(fmt.Sprintf("http://%s", proxyAddr))
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	resp, err := client.Get("http://nx.test/")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestProxy_Addr(t *testing.T) {
	proxy, err := NewProxy(context.Background(), &Policy{DefaultDeny: true})
	require.NoError(t, err)
	assert.Empty(t, proxy.Addr())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = proxy.Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	require.Eventually(t, func() bool { return proxy.Addr() == ln.Addr().String() }, time.Second, 10*time.Millisecond)
}

func TestProxy_DialsPinnedIP(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	require.NoError(t, err)

	var calls atomic.Int32
	lookup := func(string) ([]net.IP, error) {
		if calls.Add(1) == 1 {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		// A re-resolution at dial time would land here and fail.
		return []net.IP{net.ParseIP("127.0.0.2")}, nil
	}

	pol := &Policy{DefaultDeny: true, AllowLocalNetwork: true}
	_, proxyAddr := newTestProxy(t, pol, lookup)

	proxyURL, err := url.Parse(fmt.Sprintf("http://%s", proxyAddr))
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	resp, err := client.Get(fmt.Sprintf("http://pinned.test:%s/", originURL.Port()))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(1), calls.Load(), "the destination must be resolved exactly once")
}

func TestProxy_ServeShutdown(t *testing.T) {
	proxy, err := NewProxy(context.Background(), &Policy{DefaultDeny: true})
	require.NoError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- proxy.Serve(ctx, ln)
	}()

	require.Eventually(t, func() bool { return proxy.Addr() != "" }, time.Second, 10*time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}

	_, err = ln.Accept()
	assert.Error(t, err, "listener must be closed")
}

func TestProxy_IsHTTPHandler(t *testing.T) {
	var _ http.Handler = (*Proxy)(nil)

	proxy, err := NewProxy(context.Background(), &Policy{DefaultDeny: true})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNewProxy_Validation(t *testing.T) {
	t.Run("nil policy defaults to deny", func(t *testing.T) {
		proxy, err := NewProxy(context.Background(), nil)
		require.NoError(t, err)
		assert.True(t, proxy.policy.DefaultDeny)
	})

	t.Run("invalid CIDR", func(t *testing.T) {
		_, err := NewProxy(context.Background(), &Policy{AllowCIDRs: []string{"140.82.112.0"}})
		assert.Error(t, err)
	})

	t.Run("wildcard host", func(t *testing.T) {
		_, err := NewProxy(context.Background(), &Policy{AllowHosts: []string{"*.github.com"}})
		assert.Error(t, err)
	})

	t.Run("host with port", func(t *testing.T) {
		_, err := NewProxy(context.Background(), &Policy{AllowHosts: []string{"example.com:443"}})
		assert.Error(t, err)
	})

	t.Run("typical allowlist", func(t *testing.T) {
		_, err := NewProxy(context.Background(), &Policy{
			DefaultDeny: true,
			AllowHosts:  []string{"registry.npmjs.org", "proxy.golang.org", "github.com"},
			AllowCIDRs:  []string{"140.82.112.0/20"},
		})
		assert.NoError(t, err)
	})
}

func TestNewProxy_CompileIdempotent(t *testing.T) {
	pol := &Policy{DefaultDeny: true, AllowCIDRs: []string{"140.82.112.0/20"}}

	for range 3 {
		_, err := NewProxy(context.Background(), pol)
		require.NoError(t, err)
	}

	assert.Len(t, pol.parsedCIDRs, 1)
	assert.NoError(t, pol.Allows("github.com", net.ParseIP("140.82.118.4")))
	assert.Error(t, pol.Allows("github.com", net.ParseIP("142.250.80.46")))
}

func TestProxy_EnvVarsDelegates(t *testing.T) {
	pol := &Policy{DefaultDeny: true}
	proxy, err := NewProxy(context.Background(), pol)
	require.NoError(t, err)

	assert.Equal(t, pol.ProxyEnv("http://eino-egress:3128"), proxy.EnvVars("eino-egress:3128"))
	assert.True(t, strings.Contains(proxy.EnvVars("eino-egress:3128")["NO_PROXY"], "eino-egress"))
}
