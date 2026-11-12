package dagger

import (
	"context"
	"net"
	"testing"

	"dagger.io/dagger"
	"emperror.dev/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/egress"
)

func TestPolicyFingerprint(t *testing.T) {
	base := &egress.Policy{
		AllowHosts:        []string{"github.com", "proxy.golang.org"},
		AllowCIDRs:        []string{"140.82.112.0/20", "10.0.0.0/8"},
		AllowLocalNetwork: true,
		DefaultDeny:       true,
	}

	t.Run("equivalent content matches", func(t *testing.T) {
		reordered := &egress.Policy{
			AllowHosts:        []string{"PROXY.GOLANG.ORG", "GitHub.com"},
			AllowCIDRs:        []string{"10.0.0.0/8", "140.82.112.0/20"},
			AllowLocalNetwork: true,
			DefaultDeny:       true,
		}
		assert.Equal(t, policyFingerprint(base), policyFingerprint(reordered))
	})

	t.Run("each enforcement field changes the fingerprint", func(t *testing.T) {
		variants := []*egress.Policy{
			{AllowHosts: []string{"github.com"}, AllowCIDRs: base.AllowCIDRs, AllowLocalNetwork: true, DefaultDeny: true},
			{AllowHosts: base.AllowHosts, AllowCIDRs: []string{"140.82.112.0/20"}, AllowLocalNetwork: true, DefaultDeny: true},
			{AllowHosts: base.AllowHosts, AllowCIDRs: base.AllowCIDRs, AllowLocalNetwork: false, DefaultDeny: true},
			{AllowHosts: base.AllowHosts, AllowCIDRs: base.AllowCIDRs, AllowLocalNetwork: true, DefaultDeny: false},
		}
		for _, variant := range variants {
			assert.NotEqual(t, policyFingerprint(base), policyFingerprint(variant))
		}
	})

	t.Run("input slices are not reordered", func(t *testing.T) {
		hosts := []string{"github.com", "proxy.golang.org"}
		cidrs := []string{"140.82.112.0/20", "10.0.0.0/8"}
		pol := &egress.Policy{AllowHosts: hosts, AllowCIDRs: cidrs, DefaultDeny: true}
		_ = policyFingerprint(pol)
		assert.Equal(t, []string{"github.com", "proxy.golang.org"}, hosts)
		assert.Equal(t, []string{"140.82.112.0/20", "10.0.0.0/8"}, cidrs)
	})
}

func TestEgressProxyService_NotConnected(t *testing.T) {
	c := &Client{}
	_, err := c.EgressProxyService(context.Background(), &egress.Policy{DefaultDeny: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not connected")
}

func TestEgressProxyService_NilPolicy(t *testing.T) {
	c := &Client{client: &dagger.Client{}}
	_, err := c.EgressProxyService(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "egress policy is nil")
}

func TestEgressProxyService_AfterClose(t *testing.T) {
	c := &Client{client: &dagger.Client{}}
	require.NoError(t, c.Close())

	_, err := c.EgressProxyService(context.Background(), &egress.Policy{DefaultDeny: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not connected")
	assert.Empty(t, c.proxies, "a closed client must not leak a proxy into a fresh map")
}

func TestContainer_NilPolicyIsNoOp(t *testing.T) {
	cc := &containerConfig{}
	WithWorkspace("/tmp")(cc)
	WithCacheVolume("/cache", "key")(cc)
	WithEgressPolicy(nil)(cc)
	assert.Nil(t, cc.egressPolicy)

	c := &Client{}
	assert.Empty(t, c.proxies)

	_, err := c.Container(context.Background(), "alpine:3.20", WithEgressPolicy(&egress.Policy{DefaultDeny: true}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not connected")
	assert.Empty(t, c.proxies)
}

func TestErrEgressProxyUnavailable_Is(t *testing.T) {
	base := errors.New("listen failed")
	err := errors.WithMessage(
		errors.Append(ErrEgressProxyUnavailable, base),
		"failed to listen for the egress proxy",
	)
	assert.True(t, errors.Is(err, ErrEgressProxyUnavailable))
}

func TestErrEgressProxyUnavailable_ThroughContainerWrap(t *testing.T) {
	// Container() wraps the EgressProxyService error with
	// errors.Wrap(err, "failed to build egress proxy service"); the sentinel
	// must survive that full chain so callers can branch with errors.Is.
	listenErr := errors.WithMessage(
		errors.Append(ErrEgressProxyUnavailable, errors.New("listen failed")),
		"failed to listen for the egress proxy",
	)
	err := errors.Wrap(listenErr, "failed to build egress proxy service")
	assert.True(t, errors.Is(err, ErrEgressProxyUnavailable))
}

func TestProxyEnv_NoProxyContainsAlias(t *testing.T) {
	pol := &egress.Policy{DefaultDeny: true}
	assert.Contains(t, pol.ProxyEnv(egressProxyURL)["NO_PROXY"], egressServiceAlias)
}

func TestClientClose_StopsProxies(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
	}()

	c := &Client{
		proxies: map[string]*proxyInstance{
			"fingerprint": {
				listener: ln,
				cancel:   cancel,
				done:     done,
			},
		},
	}

	require.NoError(t, c.Close())
	assert.Empty(t, c.proxies)

	select {
	case <-done:
	default:
		t.Fatal("proxy goroutine was not stopped")
	}

	_, err = ln.Accept()
	assert.ErrorIs(t, err, net.ErrClosed)

	require.NoError(t, c.Close())
}
