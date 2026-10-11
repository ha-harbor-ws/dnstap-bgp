package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/BurntSushi/toml"
)

type cfgRoot struct {
	Domains             string `toml:"domains"`
	ProxyDomains        string `toml:"proxy_domains"`
	DirectDomains       string `toml:"direct_domains"`
	LegacyProxyDomains  string `toml:"blocked_domains"`
	LegacyDirectDomains string `toml:"unblocked_domains"`
	Cache               string
	TTL                 string
	IPv6                bool

	DNSTap *dnstapCfg
	BGP    *bgpCfg
	Syncer *syncerCfg
}

var (
	version string
)

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func main() {
	var (
		bgp    *bgpServer
		ipDB   *db
		syncer *syncer

		err      error
		shutdown = make(chan struct{})
	)

	log.Printf("dnstap-bgp v%s", version)

	config := flag.String("config", "", "Path to a config file")
	flag.Parse()

	if *config == "" {
		log.Fatal("You need to specify path to a config file")
	}

	cfg := &cfgRoot{}
	if _, err = toml.DecodeFile(*config, &cfg); err != nil {
		log.Fatalf("Unable to parse config file '%s': %s", *config, err)
	}

	cfg.DNSTap.IPv6 = cfg.IPv6
	cfg.BGP.IPv6 = cfg.IPv6

	proxyPath := firstNonEmpty(cfg.ProxyDomains, cfg.LegacyProxyDomains, cfg.Domains)
	directPath := firstNonEmpty(cfg.DirectDomains, cfg.LegacyDirectDomains)
	if proxyPath == "" && directPath == "" {
		log.Fatal("You need to specify proxy_domains and/or direct_domains")
	}

	ttl := 24 * time.Hour
	if cfg.TTL != "" {
		if ttl, err = time.ParseDuration(cfg.TTL); err != nil {
			log.Printf("Unable to parse TTL: %s", err)
		}
	}

	announce := func(ip net.IP, class domainClass) {
		if err := bgp.addHost(ip, class); err != nil {
			log.Printf("Unable to announce %s (%s): %s", ip, class, err)
		}
	}

	withdraw := func(e *cacheEntry) {
		if err := bgp.delHost(e.IP, e.Class); err != nil {
			log.Printf("Unable to withdraw %s (%s): %s", e.IP, e.Class, err)
		}

		if ipDB != nil {
			ipDB.del(e.IP)
		}
	}

	expireCb := func(e *cacheEntry) {
		log.Printf("%s (%s) expired", e.IP, e.Domain)
		withdraw(e)
	}

	ipCache := newCache(ttl, expireCb)
	dLists, pN, pS, dN, dS, err := loadDomainLists(proxyPath, directPath)
	if err != nil {
		log.Fatalf("Unable to load domain lists: %s", err)
	}

	log.Printf("Proxy domains loaded: %d, skipped: %d", pN, pS)
	log.Printf("Direct domains loaded: %d, skipped: %d", dN, dS)

	if bgp, err = newBgp(cfg.BGP); err != nil {
		log.Fatalf("Unable to init BGP: %s", err)
	}

	if bgp.proxyComm.empty() && bgp.directComm.empty() {
		log.Printf("Warning: no BGP communities in [bgp]; neighbors will get empty clist")
	} else {
		log.Printf("BGP proxy communities: %s", bgp.proxyComm)
		log.Printf("BGP direct communities: %s", bgp.directComm)
	}

	if cfg.Cache != "" {
		if ipDB, err = newDB(cfg.Cache); err != nil {
			log.Fatalf("Unable to init DB '%s': %s", cfg.Cache, err)
		}

		es, err := ipDB.fetchAll()
		if err != nil {
			log.Fatalf("Unable to load entries from DB: %s", err)
		}

		now := time.Now()
		i, j, k := 0, 0, 0
		for _, e := range es {
			if now.Sub(e.TS) >= ttl {
				ipDB.del(e.IP)
				j++
				continue
			}

			class, ok := dLists.classOf(e.Domain)
			if !ok {
				ipDB.del(e.IP)
				k++
				continue
			}

			e.Class = class
			ipCache.add(e)
			announce(e.IP, class)
			i++
		}

		log.Printf("Loaded from DB: %d, expired: %d, vanished: %d", i, j, k)
	}

	ipDBPut := func(e *cacheEntry) {
		if ipDB == nil {
			return
		}

		if err := ipDB.add(e); err != nil {
			log.Printf("Unable to add (%s, %s) to DB: %s", e.IP, e.Domain, err)
		}
	}

	addEntry := func(e *cacheEntry, touch bool) bool {
		if e.Class == "" {
			if class, ok := dLists.classOf(e.Domain); ok {
				e.Class = class
			} else {
				e.Class = domainProxy
			}
		} else {
			e.Class = normalizeClass(e.Class)
		}

		if prev := ipCache.lookup(e.IP, touch); prev != nil {
			if touch {
				ipDBPut(prev)
			}
			if prev.Class != e.Class || prev.Domain != e.Domain {
				log.Printf("%s: %s class %s -> %s", e.Domain, e.IP, prev.Class, e.Class)
				if err := bgp.delHost(prev.IP, prev.Class); err != nil {
					log.Printf("Unable to withdraw %s (%s): %s", prev.IP, prev.Class, err)
				}
				ipCache.add(e)
				announce(e.IP, e.Class)
				ipDBPut(e)
			}
			return false
		}

		log.Printf("%s: %s (%s, from peer: %t)", e.Domain, e.IP, e.Class, !touch)
		announce(e.IP, e.Class)
		ipCache.add(e)
		ipDBPut(e)

		return true
	}

	if cfg.Syncer != nil {
		if cfg.Syncer.Listen != "" || len(cfg.Syncer.Peers) > 0 {
			syncerCb := func(peer string, new int, err error) {
				log.Printf("Syncer: Peer %s: synced: %d error: %v", peer, new, err)
			}

			if syncer, err = newSyncer(cfg.Syncer, ipCache.getAll, addEntry, syncerCb); err != nil {
				log.Fatalf("Unable to init syncer: %s", err)
			}
		}
	}

	addHostCb := func(ip net.IP, domain string) {
		class, ok := dLists.classOf(domain)
		if !ok {
			return
		}

		e := &cacheEntry{
			IP:     ip,
			Domain: domain,
			TS:     time.Now(),
			Class:  class,
		}

		addEntry(e, true)

		if syncer != nil {
			if err := syncer.broadcast(e); err != nil {
				log.Printf("Unable to broadcast [%s %s]: %s", ip, domain, err)
			}
		}
	}

	dnsTapErrorCb := func(err error) {
		log.Printf("DNSTap error: %s", err)
	}

	if _, err = newDnstapServer(cfg.DNSTap, addHostCb, dnsTapErrorCb); err != nil {
		log.Fatalf("Unable to init DNSTap: %s", err)
	}

	log.Printf("Listening for DNSTap on: %s", cfg.DNSTap.Listen)

	go func() {
		sigchannel := make(chan os.Signal, 1)
		signal.Notify(sigchannel, notifySignals()...)

		for sig := range sigchannel {
			switch sig {
			case sigReload:
				tmp, rpN, rpS, rdN, rdS, err := loadDomainLists(proxyPath, directPath)
				if err != nil {
					log.Printf("Unable to load domain lists: %s", err)
					continue
				}

				dLists.swap(tmp)
				log.Printf("Proxy domains loaded: %d, skipped: %d", rpN, rpS)
				log.Printf("Direct domains loaded: %d, skipped: %d", rdN, rdS)

				n := ipCache.purgeUnmatched(dLists.has, func(e *cacheEntry) {
					log.Printf("%s (%s) vanished after reload", e.IP, e.Domain)
					withdraw(e)
				})
				if n > 0 {
					log.Printf("Purged after reload: %d", n)
				}

				for _, e := range ipCache.getAll() {
					class, ok := dLists.classOf(e.Domain)
					if !ok || e.Class == class {
						continue
					}
					log.Printf("%s (%s) class %s -> %s after reload", e.IP, e.Domain, e.Class, class)
					if err := bgp.delHost(e.IP, e.Class); err != nil {
						log.Printf("Unable to withdraw %s (%s): %s", e.IP, e.Class, err)
					}
					e.Class = class
					announce(e.IP, class)
					ipDBPut(e)
				}

			case os.Interrupt, sigTerm:
				close(shutdown)

			case sigStats:
				log.Printf("IPs exported: %d, proxy domains: %d, direct domains: %d",
					ipCache.count(), dLists.proxy.count(), dLists.direct.count())
			}
		}
	}()

	<-shutdown
	bgp.close()

	if syncer != nil {
		syncer.close()
	}

	if ipDB != nil {
		ipDB.close()
	}
}
