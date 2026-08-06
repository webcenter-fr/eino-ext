package egress

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"emperror.dev/errors"
)

// Proxy is an HTTP/HTTPS CONNECT proxy that enforces egress policies.
type Proxy struct {
	policy  *Policy
	server  *http.Server
	addr    string
}

// NewProxy creates a new Proxy with the given egress policy.
func NewProxy(pol *Policy) (*Proxy, error) {
	if pol == nil {
		pol = &Policy{DefaultDeny: true}
	}
	if err := pol.compile(); err != nil {
		return nil, err
	}
	return &Proxy{policy: pol}, nil
}

// Serve starts the proxy server on the given listener and blocks until ctx is done.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleConnect)

	p.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.server.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return p.server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Addr returns the address the proxy server is listening on.
func (p *Proxy) Addr() string {
	return p.addr
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "only CONNECT method supported", http.StatusMethodNotAllowed)
		return
	}

	dest := r.URL.Host
	if dest == "" {
		dest = r.Host
	}
	if dest == "" {
		http.Error(w, "no destination host", http.StatusBadRequest)
		return
	}

	host, _, err := net.SplitHostPort(dest)
	if err != nil {
		host = dest
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		http.Error(w, "DNS resolution failed", http.StatusBadGateway)
		return
	}

	for _, ip := range ips {
		if polErr := p.policy.Allows(host, ip); polErr != nil {
			http.Error(w, polErr.Error(), http.StatusForbidden)
			return
		}
	}

	destConn, err := net.DialTimeout("tcp", dest, 10*time.Second)
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

	go transfer(clientConn, destConn)
	go transfer(destConn, clientConn)
}

func transfer(dst, src net.Conn) {
	defer func() { _ = dst.Close() }()
	defer func() { _ = src.Close() }()

	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// EnvVars returns environment variables that configure the proxy for downstream processes.
func (p *Proxy) EnvVars(listenAddr string) map[string]string {
	proxyURL := "http://" + listenAddr
	return map[string]string{
		"HTTP_PROXY":  proxyURL,
		"HTTPS_PROXY": proxyURL,
		"http_proxy":  proxyURL,
		"https_proxy": proxyURL,
		"NO_PROXY":    "",
		"no_proxy":    "",
	}
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
