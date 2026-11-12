package egress

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Proxy is an HTTP/HTTPS proxy that enforces an egress Policy. It handles
// CONNECT (HTTPS tunneling) and absolute-form plain HTTP requests.
//
// Proxy implements http.Handler directly. It must NOT be registered on an
// http.ServeMux: ServeMux routes by path and returns 404 for CONNECT, whose
// request target is in authority form.
type Proxy struct {
	policy   *Policy
	server   *http.Server
	addr     string
	mu       sync.RWMutex // guards addr
	lookupIP func(string) ([]net.IP, error)

	connsMu sync.Mutex
	conns   map[net.Conn]struct{} // hijacked CONNECT tunnels, closed on shutdown
}

var _ http.Handler = (*Proxy)(nil)

// NewProxy creates a Proxy enforcing pol. A nil pol defaults to
// &Policy{DefaultDeny: true}. The policy is validated and compiled.
func NewProxy(ctx context.Context, pol *Policy) (*Proxy, error) {
	if pol == nil {
		pol = &Policy{DefaultDeny: true}
	}
	if err := validate.Struct(pol); err != nil {
		return nil, err
	}
	if err := pol.compile(); err != nil {
		return nil, err
	}
	return &Proxy{policy: pol, lookupIP: net.LookupIP}, nil
}

// ServeHTTP dispatches CONNECT to the tunnel path and every other method to
// the plain-HTTP forward path.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleForward(w, r)
}

// Serve serves the proxy on ln and blocks until ctx is done or the server
// fails. It records ln.Addr() so Addr() is accurate for the caller.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	p.mu.Lock()
	p.addr = ln.Addr().String()
	p.mu.Unlock()

	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.server.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := p.server.Shutdown(shutdownCtx)
		// Hijacked CONNECT tunnels are not tracked by Shutdown; force them
		// closed so they cannot keep the process alive.
		p.closeConns()
		if err != nil {
			_ = p.server.Close()
			return err
		}
		return nil
	case err := <-errCh:
		// The listener was closed externally (e.g. proxyInstance.stop closes it
		// directly), bypassing the graceful path above. Force-close hijacked
		// tunnels and idle connections so nothing leaks.
		p.closeConns()
		_ = p.server.Close()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// trackConn registers a hijacked connection so Serve can force-close it on
// shutdown. Shutdown does not track hijacked connections.
func (p *Proxy) trackConn(conn net.Conn) {
	p.connsMu.Lock()
	if p.conns == nil {
		p.conns = make(map[net.Conn]struct{})
	}
	p.conns[conn] = struct{}{}
	p.connsMu.Unlock()
}

// untrackConn removes a hijacked connection from the shutdown set.
func (p *Proxy) untrackConn(conn net.Conn) {
	p.connsMu.Lock()
	delete(p.conns, conn)
	p.connsMu.Unlock()
}

// closeConns force-closes every hijacked connection. Closing a connection
// unblocks the io.Copy goroutines that own it, which then close their peer.
func (p *Proxy) closeConns() {
	p.connsMu.Lock()
	defer p.connsMu.Unlock()
	for conn := range p.conns {
		_ = conn.Close()
	}
}

// Addr returns the address the proxy is listening on, or "" before Serve.
func (p *Proxy) Addr() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.addr
}

// EnvVars returns the proxy environment variables for a proxy reachable at
// listenAddr. It delegates to Policy.ProxyEnv.
func (p *Proxy) EnvVars(listenAddr string) map[string]string {
	return p.policy.ProxyEnv(fmt.Sprintf("http://%s", listenAddr))
}

// errPolicyDenied marks a destination rejected by the policy so handlers can
// answer 403 instead of 502.
type errPolicyDenied struct{ err error }

func (e *errPolicyDenied) Error() string { return e.err.Error() }
func (e *errPolicyDenied) Unwrap() error { return e.err }

// checkDest resolves hostport, validates every resolved IP against the policy,
// and returns the dial target pinned to the first validated IP. Pinning the IP
// closes the DNS-rebinding window between validation and dial: re-resolving at
// dial time would let a hostile DNS server return an allowed IP for the check
// and a private IP for the connection.
//
// defaultPort is used when hostport carries no port ("443" for CONNECT,
// "80" for plain HTTP).
func (p *Proxy) checkDest(hostport, defaultPort string) (dialAddr, host string, err error) {
	host, port, splitErr := net.SplitHostPort(hostport)
	if splitErr != nil {
		host = strings.Trim(hostport, "[]")
		port = defaultPort
	}
	if host == "" {
		return "", "", errors.New("no destination host")
	}

	ips, err := p.lookupIP(host)
	if err != nil {
		return "", "", errors.Wrapf(err, "DNS resolution failed for %q", host)
	}
	if len(ips) == 0 {
		return "", "", errors.Errorf("DNS resolution returned no addresses for %q", host)
	}

	for _, ip := range ips {
		if polErr := p.policy.Allows(host, ip); polErr != nil {
			return "", "", &errPolicyDenied{err: polErr}
		}
	}

	return net.JoinHostPort(ips[0].String(), port), host, nil
}

func writeDestError(w http.ResponseWriter, err error) {
	var denied *errPolicyDenied
	if errors.As(err, &denied) {
		http.Error(w, denied.Error(), http.StatusForbidden)
		return
	}
	http.Error(w, "DNS resolution failed", http.StatusBadGateway)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	dest := r.URL.Host
	if dest == "" {
		dest = r.Host
	}
	if dest == "" {
		http.Error(w, "no destination host", http.StatusBadRequest)
		return
	}

	dialAddr, _, err := p.checkDest(dest, "443")
	if err != nil {
		writeDestError(w, err)
		return
	}

	destConn, err := net.DialTimeout("tcp", dialAddr, 10*time.Second)
	if err != nil {
		http.Error(w, "failed to connect to destination", http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		_ = destConn.Close()
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijack failed", http.StatusInternalServerError)
		_ = destConn.Close()
		return
	}

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	p.trackConn(clientConn)
	p.trackConn(destConn)

	go func() {
		defer p.untrackConn(clientConn)
		defer p.untrackConn(destConn)
		_, _ = io.Copy(destConn, clientConn)
		_ = destConn.Close()
		_ = clientConn.Close()
	}()
	go func() {
		defer p.untrackConn(clientConn)
		defer p.untrackConn(destConn)
		_, _ = io.Copy(clientConn, destConn)
		_ = clientConn.Close()
		_ = destConn.Close()
	}()
}

var hopByHopHeaders = []string{
	"Proxy-Connection",
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopByHopHeaders(h http.Header) {
	for _, key := range hopByHopHeaders {
		h.Del(key)
	}
}

func (p *Proxy) handleForward(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "proxy requires absolute-form request target", http.StatusBadRequest)
		return
	}

	defaultPort := "80"
	if r.URL.Scheme == "https" {
		defaultPort = "443"
	}
	dialAddr, _, err := p.checkDest(r.URL.Host, defaultPort)
	if err != nil {
		writeDestError(w, err)
		return
	}

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	// Preserve the full authority (host:port) so non-default ports survive the
	// forward; checkDest already validated r.URL.Host against the policy.
	outReq.Host = r.URL.Host
	removeHopByHopHeaders(outReq.Header)

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", dialAddr)
		},
	}
	defer transport.CloseIdleConnections()

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "failed to reach destination", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	removeHopByHopHeaders(resp.Header)
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ValidateProxyURL checks that the given URL is a valid HTTP/HTTPS proxy URL.
func ValidateProxyURL(_ context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.Wrap(err, "invalid proxy URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("proxy URL must use http or https scheme")
	}
	return nil
}
