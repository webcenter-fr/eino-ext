package dagger

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"dagger.io/dagger"
	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/egress"
)

const (
	// egressServiceAlias is the in-container hostname that resolves to the
	// egress proxy service.
	egressServiceAlias = "eino-egress"

	// egressProxyPort is the container-visible port of the egress proxy.
	egressProxyPort = 3128

	// proxyShutdownTimeout bounds how long Close waits for each proxy
	// goroutine to finish.
	proxyShutdownTimeout = 5 * time.Second
)

// egressProxyURL is the proxy URL exported to the container environment. It is
// derived from the alias and port so the three values cannot drift.
var egressProxyURL = fmt.Sprintf("http://%s:%d", egressServiceAlias, egressProxyPort)

// ErrEgressProxyUnavailable is returned when an egress policy was requested but
// the proxy backing it could not be started. Callers must treat this as fatal:
// continuing without the proxy would silently grant unrestricted egress.
var ErrEgressProxyUnavailable = errors.Sentinel("egress proxy unavailable")

// proxyInstance is one running in-process proxy and the Dagger service that
// exposes it to containers.
type proxyInstance struct {
	proxy    *egress.Proxy
	listener net.Listener
	service  *dagger.Service
	cancel   context.CancelFunc
	done     chan struct{} // closed when the Serve goroutine returns
}

// stop cancels the proxy, closes its listener and waits for the Serve goroutine
// to return, bounded by proxyShutdownTimeout.
func (p *proxyInstance) stop() error {
	p.cancel()

	closeErr := p.listener.Close()
	if errors.Is(closeErr, net.ErrClosed) {
		closeErr = nil
	}

	select {
	case <-p.done:
		return closeErr
	case <-time.After(proxyShutdownTimeout):
		return errors.Combine(closeErr, errors.New("timed out waiting for egress proxy to stop"))
	}
}

// EgressProxyService returns a Dagger service that proxies container traffic
// through an in-process egress proxy enforcing pol.
//
// The proxy listens on client loopback (127.0.0.1, ephemeral port) and is
// exposed to containers through a host tunnel on port 3128. Services are cached
// per policy fingerprint, so repeated calls with an equivalent policy reuse one
// proxy. Every proxy is stopped by Client.Close.
func (c *Client) EgressProxyService(ctx context.Context, pol *egress.Policy) (*dagger.Service, error) {
	if c.client == nil {
		return nil, errors.New("client not connected")
	}
	if pol == nil {
		return nil, errors.New("egress policy is nil")
	}

	fingerprint := policyFingerprint(pol)

	c.proxyMu.Lock()
	if inst, ok := c.proxies[fingerprint]; ok {
		c.proxyMu.Unlock()
		return inst.service, nil
	}
	c.proxyMu.Unlock()

	proxy, err := egress.NewProxy(ctx, pol)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create egress proxy")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.WithMessage(
			errors.Append(ErrEgressProxyUnavailable, err),
			"failed to listen for the egress proxy",
		)
	}

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, errors.WithMessage(
			errors.Append(ErrEgressProxyUnavailable, errors.Errorf("unexpected listener address type %T", ln.Addr())),
			"failed to determine egress proxy port",
		)
	}

	// The proxy must outlive the Container() call that created it: the cached
	// Dagger service lives on after the tool invocation returns.
	proxyCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = proxy.Serve(proxyCtx, ln)
	}()

	svc := c.client.Host().Service(
		[]dagger.PortForward{{
			Backend:  tcpAddr.Port,
			Frontend: egressProxyPort,
			Protocol: dagger.NetworkProtocolTcp,
		}},
		dagger.HostServiceOpts{Host: "127.0.0.1"},
	)

	inst := &proxyInstance{
		proxy:    proxy,
		listener: ln,
		service:  svc,
		cancel:   cancel,
		done:     done,
	}

	c.proxyMu.Lock()
	if c.proxies == nil {
		c.proxies = make(map[string]*proxyInstance)
	}
	if existing, ok := c.proxies[fingerprint]; ok {
		c.proxyMu.Unlock()
		_ = inst.stop()
		return existing.service, nil
	}
	c.proxies[fingerprint] = inst
	c.proxyMu.Unlock()

	return svc, nil
}

// policyFingerprint returns a stable hex digest identifying a policy's
// enforcement-relevant content. Two structurally equivalent policies share one
// proxy; any difference yields a different proxy.
//
// The "v1" prefix is a schema marker: if Policy gains an enforcement-relevant
// field, bump it to "v2" and include the field, or distinct policies will
// collide onto one proxy.
func policyFingerprint(pol *egress.Policy) string {
	hosts := append([]string(nil), pol.AllowHosts...)
	for i := range hosts {
		hosts[i] = strings.ToLower(hosts[i])
	}
	sort.Strings(hosts)

	cidrs := append([]string(nil), pol.AllowCIDRs...)
	sort.Strings(cidrs)

	h := sha256.New()
	_, _ = fmt.Fprintf(h, "v1\x00%s\x00%s\x00local=%t\x00deny=%t",
		strings.Join(hosts, "\x01"),
		strings.Join(cidrs, "\x01"),
		pol.AllowLocalNetwork,
		pol.DefaultDeny,
	)
	return fmt.Sprintf("%x", h.Sum(nil))
}
