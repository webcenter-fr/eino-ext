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
	"net"
	"strings"

	"emperror.dev/errors"
)

// Policy defines the egress filtering rules for the HTTP/HTTPS proxy.
type Policy struct {
	AllowHosts        []string
	AllowCIDRs        []string
	AllowLocalNetwork bool
	DefaultDeny       bool

	parsedCIDRs []*net.IPNet
}

func (p *Policy) compile() error {
	for _, cidr := range p.AllowCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return errors.Wrapf(err, "invalid CIDR %q", cidr)
		}
		p.parsedCIDRs = append(p.parsedCIDRs, ipNet)
	}
	return nil
}

var rfc1918Blocks = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
}

var linkLocalBlocks = []string{
	"169.254.0.0/16",
}

var cloudMetadataIP = "169.254.169.254"

func isRFC1918(ip net.IP) bool {
	for _, block := range rfc1918Blocks {
		_, ipNet, _ := net.ParseCIDR(block)
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

func isLinkLocal(ip net.IP) bool {
	for _, block := range linkLocalBlocks {
		_, ipNet, _ := net.ParseCIDR(block)
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

func isLoopback(ip net.IP) bool {
	return ip.IsLoopback()
}

// Allows checks whether the given host and IP are permitted under the policy.
func (p *Policy) Allows(host string, ip net.IP) error {
	if p.DefaultDeny {
		if p.AllowLocalNetwork && (isRFC1918(ip) || isLinkLocal(ip) || isLoopback(ip)) {
			return nil
		}

		if !p.AllowLocalNetwork {
			if isRFC1918(ip) {
				return errors.Errorf("egress denied: host %q resolves to RFC1918 IP %s (AllowLocalNetwork is false)", host, ip)
			}
			if isLinkLocal(ip) {
				if ip.String() == cloudMetadataIP {
					return errors.Errorf("egress denied: host %q resolves to cloud metadata IP %s (AllowLocalNetwork is false)", host, cloudMetadataIP)
				}
				return errors.Errorf("egress denied: host %q resolves to link-local IP %s (AllowLocalNetwork is false)", host, ip)
			}
			if isLoopback(ip) {
				return errors.Errorf("egress denied: host %q resolves to loopback IP %s (AllowLocalNetwork is false)", host, ip)
			}
		}

		for _, allowed := range p.AllowHosts {
			if strings.EqualFold(host, allowed) {
				return nil
			}
		}

		for _, cidr := range p.parsedCIDRs {
			if cidr.Contains(ip) {
				return nil
			}
		}

		return errors.Errorf("egress denied: host %q (%s) is not in the allowlist", host, ip)
	}

	return nil
}
