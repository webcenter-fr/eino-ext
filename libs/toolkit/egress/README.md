// Package egress provides a policy-driven HTTP/HTTPS egress proxy that enforces
// "local network forbidden by default" with an allowlist and AllowLocalNetwork
// toggle. It is designed for use inside Dagger containers to soft-control outbound
// network access.
//
// Usage:
//
//	pol := &egress.Policy{
//	    DefaultDeny: true,
//	    AllowHosts:  []string{"registry.npmjs.org", "proxy.golang.org"},
//	}
//	proxy, err := egress.NewProxy(pol)
//	// ... start proxy on a listener ...
//
// The proxy is a soft control: tools that ignore HTTP_PROXY/HTTPS_PROXY
// environment variables can bypass it. For a hard guarantee, deploy the engine
// with nftables/iptables egress rules.
package egress
