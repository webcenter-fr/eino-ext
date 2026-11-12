// Package egress provides a policy-driven HTTP/HTTPS egress proxy that enforces
// "local network forbidden by default" with an allowlist and AllowLocalNetwork
// toggle. It is designed for use inside Dagger containers to soft-control outbound
// network access.
//
// The proxy is a soft control: tools that ignore HTTP_PROXY/HTTPS_PROXY
// environment variables can bypass it. For a hard guarantee, deploy the engine
// with nftables/iptables egress rules.
package egress

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"emperror.dev/errors"
)

// Policy defines the egress filtering rules for the HTTP/HTTPS proxy.
//
// AllowHosts entries are exact, case-insensitive hostnames. Wildcards
// ("*.github.com"), ports ("example.com:443"), and trailing dots are rejected
// at validation time: they could never match.
type Policy struct {
	AllowHosts        []string `validate:"omitempty,dive,hostname_rfc1123" jsonschema:"description=Exact case-insensitive hostnames allowed to be reached (no wildcards, no ports)"`
	AllowCIDRs        []string `validate:"omitempty,dive,cidr" jsonschema:"description=CIDR blocks whose resolved IPs are allowed"`
	AllowLocalNetwork bool     `validate:"omitempty" jsonschema:"description=Allow RFC1918/link-local/loopback/ULA destinations (default false)"`
	DefaultDeny       bool     `validate:"omitempty" jsonschema:"description=Deny everything not explicitly allowed (set true to enforce)"`

	mu          sync.Mutex   // guards parsedCIDRs
	parsedCIDRs []*net.IPNet // populated by compile()
	compiled    bool
}

// compile parses AllowCIDRs into parsedCIDRs. It is idempotent: repeated calls
// reset parsedCIDRs instead of appending (fixes unbounded growth).
func (p *Policy) compile() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	parsed := make([]*net.IPNet, 0, len(p.AllowCIDRs))
	for _, cidr := range p.AllowCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return errors.Wrapf(err, "invalid CIDR %q", cidr)
		}
		parsed = append(parsed, ipNet)
	}
	p.parsedCIDRs = parsed
	p.compiled = true
	return nil
}

// defaultNoProxy keeps the proxy host and container loopback off the proxy
// path. Entries are hostnames/IPs, not CIDRs: curl ignores CIDR entries in
// NO_PROXY, so a CIDR here would be honored by Go and silently ignored by most
// container tooling.
//
// The eino-egress alias is chosen by libs/toolkit/dagger; keep the two in sync.
const defaultNoProxy = "eino-egress,localhost,127.0.0.1,::1"

// ProxyEnv returns the proxy environment variables a container needs in order
// to route HTTP and HTTPS traffic through proxyURL.
//
// It returns HTTP_PROXY, HTTPS_PROXY, http_proxy, https_proxy, NO_PROXY and
// no_proxy. NO_PROXY always lists the proxy host itself plus container
// loopback, and nothing else: when AllowLocalNetwork is true, local
// destinations still go through the proxy, where Allows permits them, so all
// egress stays on one enforceable path.
//
// proxyURL is returned as given; it is not validated here. Callers that accept
// a URL from configuration should call ValidateProxyURL first.
func (p *Policy) ProxyEnv(proxyURL string) map[string]string {
	return map[string]string{
		"HTTP_PROXY":  proxyURL,
		"HTTPS_PROXY": proxyURL,
		"http_proxy":  proxyURL,
		"https_proxy": proxyURL,
		"NO_PROXY":    defaultNoProxy,
		"no_proxy":    defaultNoProxy,
	}
}

// cgnatBlock is RFC 6598 shared address space, routable-looking but local.
var cgnatBlock = mustCIDR("100.64.0.0/10")

// ipv6MetadataIP is the IPv6 cloud metadata endpoint (AWS).
const ipv6MetadataIP = "fd00:ec2::254"

// cloudMetadataIP is the IPv4 cloud metadata endpoint.
const cloudMetadataIP = "169.254.169.254"

func mustCIDR(cidr string) *net.IPNet {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %q: %v", cidr, err))
	}
	return ipNet
}

// normalizeIP collapses IPv4-mapped IPv6 (::ffff:a.b.c.d) to its 4-byte form so
// one check covers both families.
func normalizeIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

// isLocalIP reports whether ip belongs to a network that must not be reachable
// unless AllowLocalNetwork is set. Covers both IPv4 and IPv6:
//   - loopback          127.0.0.0/8, ::1
//   - private           RFC1918, IPv6 ULA fd00::/8
//   - link-local        169.254.0.0/16, fe80::/10 (unicast and multicast)
//   - unspecified       0.0.0.0, ::
//   - CGNAT             100.64.0.0/10
func isLocalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	return cgnatBlock.Contains(ip)
}

// isMetadataIP reports whether ip is a cloud instance-metadata endpoint.
func isMetadataIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.Equal(net.ParseIP(cloudMetadataIP)) || ip.Equal(net.ParseIP(ipv6MetadataIP))
}

// Allows checks whether the given host and IP are permitted under the policy.
func (p *Policy) Allows(host string, ip net.IP) error {
	if !p.DefaultDeny {
		return nil
	}

	ip = normalizeIP(ip)
	if ip == nil {
		return errors.Errorf("egress denied: host %q has an unresolvable or invalid IP", host)
	}

	if isLocalIP(ip) || isMetadataIP(ip) {
		if p.AllowLocalNetwork {
			return nil
		}
		switch {
		case isMetadataIP(ip):
			return errors.Errorf("egress denied: host %q resolves to cloud metadata IP %s (AllowLocalNetwork is false)", host, ip)
		case ip.IsLoopback():
			return errors.Errorf("egress denied: host %q resolves to loopback IP %s (AllowLocalNetwork is false)", host, ip)
		case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
			return errors.Errorf("egress denied: host %q resolves to link-local IP %s (AllowLocalNetwork is false)", host, ip)
		case ip.IsUnspecified():
			return errors.Errorf("egress denied: host %q resolves to unspecified address %s (AllowLocalNetwork is false)", host, ip)
		case cgnatBlock.Contains(ip):
			return errors.Errorf("egress denied: host %q resolves to shared address space (CGNAT) IP %s (AllowLocalNetwork is false)", host, ip)
		case ip.To4() != nil && ip.IsPrivate():
			return errors.Errorf("egress denied: host %q resolves to RFC1918 IP %s (AllowLocalNetwork is false)", host, ip)
		default:
			return errors.Errorf("egress denied: host %q resolves to private IP %s (AllowLocalNetwork is false)", host, ip)
		}
	}

	for _, allowed := range p.AllowHosts {
		if strings.EqualFold(host, allowed) {
			return nil
		}
	}

	p.mu.Lock()
	cidrs := p.parsedCIDRs
	p.mu.Unlock()
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return nil
		}
	}

	return errors.Errorf("egress denied: host %q (%s) is not in the allowlist", host, ip)
}
