[![CI](https://github.com/ha-harbor-ws/dnstap-bgp/actions/workflows/build.yml/badge.svg)](https://github.com/ha-harbor-ws/dnstap-bgp/actions/workflows/build.yml)

# dnstap-bgp

Fork of [blind-oracle/dnstap-bgp](https://github.com/blind-oracle/dnstap-bgp). Current release: **1.2.1**.

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
* Linux: systemd unit runs the daemon in a network namespace (`ip netns`) so a local BIRD/FRR can peer with it
* OPNsense 26.7 / FreeBSD 15: rc script runs the daemon in a **VNET jail** (the FreeBSD analog of `ip netns`) with an epair

## Signals

| Signal | Action |
| --- | --- |
| `SIGHUP` | Reload the domain list and withdraw vanished prefixes |
| `SIGUSR1` | Log the number of exported IPs and loaded domains |
| `SIGTERM` / `SIGINT` | Graceful shutdown |

`SIGHUP` / `SIGUSR1` are Unix-only. On Windows only `SIGINT` / `SIGTERM` are handled.

## File paths

### OPNsense 26.7 / FreeBSD (package `os-dnstap-bgp`)

| Role | Path |
| --- | --- |
| Binary | `/usr/local/sbin/dnstap-bgp` |
| Main config | `/usr/local/etc/dnstap-bgp/dnstap-bgp.conf` |
| Main config sample | `/usr/local/etc/dnstap-bgp/dnstap-bgp.conf.sample` |
| **Domain list (default)** | **`/usr/local/etc/dnstap-bgp/domains.txt`** |
| Domain list sample | `/usr/local/etc/dnstap-bgp/domains.txt.sample` |
| rc.d service | `/usr/local/etc/rc.d/dnstap_bgp` |
| rc knobs (enable, VNET, epair IPs) | `/usr/local/etc/rc.conf.d/dnstap_bgp` |
| rc knobs sample | `/usr/local/etc/rc.conf.d/dnstap_bgp.sample` |
| BoltDB cache | `/var/db/dnstap-bgp/cache.db` |
| DNSTap UNIX socket | `/var/run/dnstap-bgp/dnstap.sock` |
| PID file | `/var/run/dnstap_bgp.pid` |
| Optional sync TLS cert | `/usr/local/etc/dnstap-bgp/sync.crt` |
| Optional sync TLS key | `/usr/local/etc/dnstap-bgp/sync.key` |

`dnstap-bgp.service` is **not** an OPNsense file. It is a **Linux systemd** unit. OPNsense uses `/usr/local/etc/rc.d/dnstap_bgp`.

### Linux (deb/rpm / systemd)

| Role | Path |
| --- | --- |
| Binary | `/usr/bin/dnstap-bgp` |
| systemd unit | `/usr/lib/systemd/system/dnstap-bgp.service` |
| systemd environment | `/etc/default/dnstap-bgp` |
| Main config | `/etc/dnstap-bgp/dnstap-bgp.conf` |
| Domain list (default in sample) | `/var/cache/domains.txt` |
| BoltDB cache | `/var/cache/dnstap-bgp.db` |
| DNSTap UNIX socket | `/tmp/dnstap.sock` |
| Optional sync TLS cert/key (if you set them) | `/etc/dnstap-bgp/sync.crt`, `/etc/dnstap-bgp/sync.key` |

`/etc/default/dnstap-bgp` sets `CONFIG=/etc/dnstap-bgp/dnstap-bgp.conf` and `NAMESPACE=dtap`. The unit starts with `ip netns exec ${NAMESPACE}`.

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

See the [releases page](https://github.com/ha-harbor-ws/dnstap-bgp/releases).

### OPNsense 26.7

ABI: `FreeBSD:15:amd64`.

```
pkg add os-dnstap-bgp-1.2.1-opnsense26.7-freebsd15-amd64.pkg
```

The post-install script copies samples to:

* `/usr/local/etc/dnstap-bgp/dnstap-bgp.conf`
* `/usr/local/etc/dnstap-bgp/domains.txt`
* `/usr/local/etc/rc.conf.d/dnstap_bgp`

Edit the domain list and BGP settings, then:

```
sysrc dnstap_bgp_enable=YES
service dnstap_bgp start
```

Reload domains: `service dnstap_bgp reload`.

Point Unbound `dnstap-socket-path` (or BIND `dnstap-output`) at `/var/run/dnstap-bgp/dnstap.sock`.

Default VNET jail `dtap`:

* host epair: `192.168.113.1/30`
* jail epair: `192.168.113.2/30`

FRR/BIRD on the host should neighbor `192.168.113.2`; `dnstap-bgp` peers with `192.168.113.1`. The UNIX DNSTap socket stays on the shared filesystem, so Unbound on the host can still write to it.

To run without a jail (host network stack):

```
sysrc dnstap_bgp_vnet=NO
```

For ARM: `python deploy/opnsense/build-pkg.py --arch arm64`.

### Linux packages

Deb/rpm with *fpm*: `make rpm` or `make deb`. Create the netns before start, e.g. `ip netns add dtap`.

### From source

Go 1.27+:

```
make
```

OPNsense package (cross-compile):

```
make opnsense-pkg
```

## Configuration

Repo templates:

* Linux: [`deploy/dnstap-bgp.conf`](deploy/dnstap-bgp.conf), [`deploy/dnstap-bgp.service`](deploy/dnstap-bgp.service), [`deploy/dnstap-bgp`](deploy/dnstap-bgp) → `/etc/default/dnstap-bgp`
* OPNsense: [`deploy/opnsense/dnstap-bgp.conf.sample`](deploy/opnsense/dnstap-bgp.conf.sample), [`deploy/opnsense/rc.d/dnstap_bgp`](deploy/opnsense/rc.d/dnstap_bgp), [`deploy/opnsense/rc.conf.d/dnstap_bgp.sample`](deploy/opnsense/rc.conf.d/dnstap_bgp.sample)

If `[syncer]` is enabled, set `token`. `tlsCert` and `tlsKey` must be set together.

## DNS server examples

DNSTap is client–server: the DNS server is the client, **dnstap-bgp** is the server.

### Unbound on OPNsense

```
dnstap:
    dnstap-enable: yes
    dnstap-socket-path: "/var/run/dnstap-bgp/dnstap.sock"
    dnstap-log-client-response-messages: yes
```

### Unbound on Linux

```
dnstap:
    dnstap-enable: yes
    dnstap-socket-path: "/tmp/dnstap.sock"
    dnstap-log-client-response-messages: yes
```

**Important:** on Ubuntu, AppArmor often blocks Unbound from the DNSTap socket. Disable the profile for Unbound or fix the rules.

### BIND

DNSTap is supported since 9.11, but is often not built into distro packages. BIND also connects only via UNIX socket.

OPNsense:

```
dnstap {
    client response;
};

dnstap-output unix "/var/run/dnstap-bgp/dnstap.sock"
```

Linux:

```
dnstap {
    client response;
};

dnstap-output unix "/tmp/dnstap.sock"
```

## License

[Mozilla Public License 2.0](LICENSE)
