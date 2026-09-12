[![CI](https://github.com/ha-harbor-ws/dnstap-bgp/actions/workflows/build.yml/badge.svg)](https://github.com/ha-harbor-ws/dnstap-bgp/actions/workflows/build.yml)

# dnstap-bgp

Fork of [blind-oracle/dnstap-bgp](https://github.com/blind-oracle/dnstap-bgp). Current release: **1.2.0**.

Requires **Go 1.27**. BGP is provided by [GoBGP v4](https://github.com/osrg/gobgp).

## Overview

This daemon solves the problem of manipulating traffic based on domain names instead of IP addresses. It intercepts DNS replies and exports the resolved IPs over BGP. Peers can then use that information to influence the traffic flow.

## Workflow

* Client sends a DNS request to a recursive DNS server that supports DNSTap (**Unbound**, **BIND**, etc.)
* The DNS server logs the reply through DNSTap to **dnstap-bgp**
* **dnstap-bgp** caches the IPs from the reply and announces them through BGP
* When a request for an already cached IP comes again, its TTL is refreshed

## Features

* Load a list of domains to intercept; a prefix tree matches subdomains
* Hot-reload of the domain list on `SIGHUP`; IPs for removed domains are withdrawn from BGP and dropped from the cache/DB
* IPv6 in DNS (AAAA), BGP and syncer
* CNAMEs are resolved and stored as separate IP → domain entries
* Export routes to any number of BGP peers
* Configurable timeout to purge entries from the cache
* Persist the cache on disk (BoltDB)
* Sync obtained IPs with other **dnstap-bgp** instances over authenticated HTTP(S)
* Linux: can run in a dedicated network namespace via `ip netns` — see `deploy/*` systemd units. Useful when a BGP router on the same host cannot peer with its own IPs (at least BIRD)
* OPNsense 26.7 / FreeBSD 15 package (`os-dnstap-bgp`)

## Signals

| Signal | Action |
| --- | --- |
| `SIGHUP` | Reload the domain list and withdraw vanished prefixes |
| `SIGUSR1` | Log the number of exported IPs and loaded domains |
| `SIGTERM` / `SIGINT` | Graceful shutdown |

`SIGHUP` / `SIGUSR1` are Unix-only. On Windows only `SIGINT` / `SIGTERM` are handled.

## Synchronization

**dnstap-bgp** can push obtained IPs to other instances and periodically fetch their caches after outages. The API is JSON over HTTP or HTTPS.

A shared `syncer.token` is **required** when listen or peers are set. Clients send `Authorization: Bearer <token>`. Optional TLS: `tlsCert` / `tlsKey`; for self-signed certs use `tlsInsecureSkipVerify`.

Without a token the process refuses to start the syncer.

## Limitations

* IDN (punycode) domain names are currently not supported and are silently skipped
* Full sync fetches the whole peer cache, so millions of entries are hard on memory and network
* The domain list and IP cache live in RAM
* Logs only to stdout (on OPNsense the rc script sends that to syslog)

## Installation

### GitHub releases

See the [releases page](https://github.com/ha-harbor-ws/dnstap-bgp/releases) for tagged builds.

### OPNsense 26.7

Install the `os-dnstap-bgp-*-opnsense26.7-freebsd15-amd64.pkg` asset (ABI `FreeBSD:15:amd64`):

```
pkg add os-dnstap-bgp-1.2.0-opnsense26.7-freebsd15-amd64.pkg
```

Edit `/usr/local/etc/dnstap-bgp/dnstap-bgp.conf` and `domains.txt`, then:

```
sysrc dnstap_bgp_enable=YES
service dnstap_bgp start
```

Reload domains without restart: `service dnstap_bgp reload`.

Point Unbound/BIND `dnstap-output` at `/var/run/dnstap-bgp/dnstap.sock`.

For ARM appliances build locally: `python deploy/opnsense/build-pkg.py --arch arm64`.

### Linux packages

Deb/rpm can be built with *fpm*: `make rpm` or `make deb`.

### From source

Go 1.27+ is required:

```
make
```

OPNsense/FreeBSD package from a Linux or Windows host (cross-compile):

```
make opnsense-pkg
```

## Configuration

* Linux / generic: [`deploy/dnstap-bgp.conf`](deploy/dnstap-bgp.conf)
* OPNsense / FreeBSD: [`deploy/opnsense/dnstap-bgp.conf.sample`](deploy/opnsense/dnstap-bgp.conf.sample)

If `[syncer]` is enabled, set `token`. `tlsCert` and `tlsKey` must be set together.

## DNS server examples

DNSTap is client–server: the DNS server is the client, **dnstap-bgp** is the server.

### Unbound

Unbound can use DNSTap only through UNIX sockets.

```
dnstap:
    dnstap-enable: yes
    dnstap-socket-path: "/tmp/dnstap.sock"
    dnstap-log-client-response-messages: yes
```

On OPNsense use `/var/run/dnstap-bgp/dnstap.sock`.

**Important:** on Ubuntu, AppArmor often blocks Unbound from the DNSTap socket. Disable the profile for Unbound or fix the rules.

### BIND

DNSTap is supported since 9.11, but is often not built into distro packages. BIND also connects only via UNIX socket.

```
dnstap {
    client response;
};

dnstap-output unix "/tmp/dnstap.sock"
```

## License

[Mozilla Public License 2.0](LICENSE)
