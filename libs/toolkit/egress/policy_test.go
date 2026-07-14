package egress

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicy_Allows_DefaultDeny(t *testing.T) {
	pol := &Policy{DefaultDeny: true}
	require.NoError(t, pol.compile())

	tests := []struct {
		name    string
		host    string
		ip      net.IP
		wantErr bool
	}{
		{
			name:    "public IP denied by default",
			host:    "google.com",
			ip:      net.ParseIP("142.250.80.46"),
			wantErr: true,
		},
		{
			name:    "RFC1918 denied by default",
			host:    "internal.local",
			ip:      net.ParseIP("10.0.0.5"),
			wantErr: true,
		},
		{
			name:    "link-local denied by default",
			host:    "169.254.169.254",
			ip:      net.ParseIP("169.254.169.254"),
			wantErr: true,
		},
		{
			name:    "loopback denied by default",
			host:    "localhost",
			ip:      net.ParseIP("127.0.0.1"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pol.Allows(tt.host, tt.ip)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPolicy_Allows_AllowLocalNetwork(t *testing.T) {
	pol := &Policy{DefaultDeny: true, AllowLocalNetwork: true}
	require.NoError(t, pol.compile())

	tests := []struct {
		name string
		host string
		ip   net.IP
	}{
		{name: "RFC1918 allowed", host: "internal", ip: net.ParseIP("10.0.0.5")},
		{name: "RFC1918 172.16", host: "internal", ip: net.ParseIP("172.16.0.1")},
		{name: "RFC1918 192.168", host: "internal", ip: net.ParseIP("192.168.1.1")},
		{name: "link-local allowed", host: "169.254.1.1", ip: net.ParseIP("169.254.1.1")},
		{name: "cloud metadata allowed", host: "169.254.169.254", ip: net.ParseIP("169.254.169.254")},
		{name: "loopback allowed", host: "localhost", ip: net.ParseIP("127.0.0.1")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pol.Allows(tt.host, tt.ip)
			assert.NoError(t, err)
		})
	}

	t.Run("public IP still denied with AllowLocalNetwork", func(t *testing.T) {
		err := pol.Allows("google.com", net.ParseIP("142.250.80.46"))
		assert.Error(t, err)
	})
}

func TestPolicy_Allows_Allowlist(t *testing.T) {
	pol := &Policy{
		DefaultDeny: true,
		AllowHosts:  []string{"registry.npmjs.org", "proxy.golang.org"},
		AllowCIDRs:  []string{"140.82.112.0/20"},
	}
	require.NoError(t, pol.compile())

	tests := []struct {
		name    string
		host    string
		ip      net.IP
		wantErr bool
	}{
		{
			name:    "allowed host exact match",
			host:    "registry.npmjs.org",
			ip:      net.ParseIP("104.16.27.34"),
			wantErr: false,
		},
		{
			name:    "allowed host case insensitive",
			host:    "REGISTRY.NPMJS.ORG",
			ip:      net.ParseIP("104.16.27.34"),
			wantErr: false,
		},
		{
			name:    "allowed CIDR match",
			host:    "github.com",
			ip:      net.ParseIP("140.82.118.4"),
			wantErr: false,
		},
		{
			name:    "unlisted host denied",
			host:    "evil.com",
			ip:      net.ParseIP("93.184.216.34"),
			wantErr: true,
		},
		{
			name:    "CIDR mismatch denied",
			host:    "google.com",
			ip:      net.ParseIP("142.250.80.46"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pol.Allows(tt.host, tt.ip)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPolicy_Allows_NonDefaultDeny(t *testing.T) {
	pol := &Policy{DefaultDeny: false}
	require.NoError(t, pol.compile())

	err := pol.Allows("google.com", net.ParseIP("142.250.80.46"))
	assert.NoError(t, err)
}

func TestPolicy_CloudMetadataDenied(t *testing.T) {
	pol := &Policy{DefaultDeny: true}
	require.NoError(t, pol.compile())

	err := pol.Allows("169.254.169.254", net.ParseIP("169.254.169.254"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cloud metadata")
}
