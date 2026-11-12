# `libs/toolkit/egress`

A policy-driven HTTP/HTTPS egress proxy. It enforces a deny-by-default
allowlist and blocks local networks (loopback, RFC1918, IPv6 ULA, link-local,
CGNAT, unspecified) plus cloud metadata endpoints. It is designed to
soft-control outbound traffic from Dagger containers.

## Policy

```go
pol := &egress.Policy{
    DefaultDeny: true,
    AllowHosts:  []string{"registry.npmjs.org", "proxy.golang.org"},
    AllowCIDRs:  []string{"140.82.112.0/20"},
}
```

| Field | Default | Semantics |
|---|---|---|
| `AllowHosts` | empty | Exact, case-insensitive hostnames. Wildcards (`*.github.com`), ports (`example.com:443`) and trailing dots are **validation errors**: they could never match. |
| `AllowCIDRs` | empty | CIDR blocks whose resolved IPs are allowed. Invalid CIDRs are validation errors. |
| `AllowLocalNetwork` | `false` | Allow loopback, RFC1918, IPv6 ULA, link-local, CGNAT and unspecified destinations. |
| `DefaultDeny` | `false` | Enforce the policy. When `false`, `Allows` permits everything and the proxy is a transparent relay. |

`Policy.Allows(host, ip)` checks **every** resolved IP. A host that resolves to
several addresses is denied if any one of them is denied.

## Running the proxy

```go
proxy, err := egress.NewProxy(ctx, pol)
if err != nil {
    return err
}

ln, err := net.Listen("tcp", "127.0.0.1:0")
if err != nil {
    return err
}
go func() { _ = proxy.Serve(ctx, ln) }()

fmt.Println(proxy.Addr()) // ln.Addr().String()
```

`NewProxy` applies the nil default (`&Policy{DefaultDeny: true}`), validates the
policy with `libs/toolkit/validate`, then compiles the CIDRs. `compile` is
idempotent: repeated calls reset the parsed CIDRs instead of appending.

`Proxy` implements `http.Handler` directly and handles both `CONNECT` tunnels
and absolute-form plain HTTP requests. **Never register it on an
`http.ServeMux`**: `ServeMux` routes by path and returns 404 for `CONNECT`,
whose request target is in authority form.

## Proxy environment

`Policy.ProxyEnv(proxyURL)` returns the six variables a container needs:

| Key | Value |
|---|---|
| `HTTP_PROXY` | `proxyURL` |
| `HTTPS_PROXY` | `proxyURL` |
| `http_proxy` | `proxyURL` |
| `https_proxy` | `proxyURL` |
| `NO_PROXY` | `eino-egress,localhost,127.0.0.1,::1` |
| `no_proxy` | same as `NO_PROXY` |

`NO_PROXY` is independent of `AllowLocalNetwork`: local destinations still go
through the proxy, where `Allows` permits them, so all egress stays on one
enforceable path. Entries are hostnames/IPs, not CIDRs, because curl ignores
CIDR entries in `NO_PROXY`.

`Proxy.EnvVars(listenAddr)` delegates to `Policy.ProxyEnv` with
`http://<listenAddr>`.

## Security

The proxy enforces:

- Deny-by-default when `DefaultDeny=true`; only `AllowHosts` and `AllowCIDRs`
  pass.
- Local networks blocked for both address families: loopback, RFC1918, IPv6 ULA
  `fd00::/8`, link-local `169.254/16` + `fe80::/10`, unspecified, and CGNAT
  `100.64.0.0/10`.
- Cloud metadata `169.254.169.254` and `fd00:ec2::254` denied with a dedicated
  message.
- IPv4-mapped IPv6 (`::ffff:10.0.0.5`) normalized before checks.
- Every resolved IP checked, not just the first.
- The validated IP is pinned and dialed directly, closing the DNS-rebinding
  window between validation and connection.

> **The proxy is a soft control.** `HTTP_PROXY` is a convention honored by HTTP
> clients. Raw TCP tools (`nc`, `psql`, `redis-cli`, `kubectl`, `ssh`, direct
> dialers) ignore it and reach the network unfiltered. This was verified from
> inside a policy-bound container: `nc -z -w 5 github.com 443` succeeded even
> though the policy denied `github.com`. A process can also unset `HTTP_PROXY`
> or pass `--noproxy '*'`. For a hard guarantee, enforce egress with
> operator-side nftables/iptables or a Kubernetes `NetworkPolicy` on the engine.

The proxy logs nothing: no URLs, headers, or bodies. Proxied requests routinely
carry `Authorization` headers and tokens in query strings. Denial detail goes
only to the requesting client as a `403` body.

The proxy runs in the calling process, not in a container. It binds
`127.0.0.1` only, so only the Dagger host tunnel can reach it.
