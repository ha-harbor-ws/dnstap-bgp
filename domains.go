package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/armon/go-radix"
)

var (
	reDomain = regexp.MustCompile(`^([a-z0-9]+(-[a-z0-9]+)*\.)+[a-z]{2,}$`)
)

func domainReverse(d string) (dr string) {
	e := strings.Split(d, ".")
	for i := 0; i < len(e)/2; i++ {
		j := len(e) - i - 1
		e[i], e[j] = e[j], e[i]
	}

	return strings.Join(e, ".")
}

func domainLevel(d string) (l int) {
	for _, c := range d {
		if c == '.' {
			l++
		}
	}

	return l + 1
}

type domainTree struct {
	t *radix.Tree
	sync.RWMutex
}

type domainClass string

const (
	domainProxy  domainClass = "proxy"
	domainDirect domainClass = "direct"
	domainBlock  domainClass = "block"
)

func normalizeClass(c domainClass) domainClass {
	switch c {
	case domainProxy, "blocked":
		return domainProxy
	case domainDirect, "unblocked":
		return domainDirect
	case domainBlock:
		return domainBlock
	default:
		return c
	}
}

// classRank: higher wins when one IP is seen for several domains (CDN).
// block > proxy > direct — never downgrade an exported prefix.
func classRank(c domainClass) int {
	switch normalizeClass(c) {
	case domainBlock:
		return 3
	case domainProxy:
		return 2
	case domainDirect:
		return 1
	default:
		return 0
	}
}

func preferClass(a, b domainClass) domainClass {
	an := normalizeClass(a)
	bn := normalizeClass(b)
	if classRank(bn) > classRank(an) {
		return bn
	}
	return an
}

type domainLists struct {
	proxy  *domainTree
	direct *domainTree
	block  *domainTree
}

func newDomainLists() *domainLists {
	return &domainLists{
		proxy:  newDomainTree(),
		direct: newDomainTree(),
		block:  newDomainTree(),
	}
}

func (d *domainLists) classOf(s string) (domainClass, bool) {
	if d.block.has(s) {
		return domainBlock, true
	}
	if d.proxy.has(s) {
		return domainProxy, true
	}
	if d.direct.has(s) {
		return domainDirect, true
	}
	return "", false
}

func (d *domainLists) has(s string) bool {
	_, ok := d.classOf(s)
	return ok
}

func (d *domainLists) count() int {
	return d.proxy.count() + d.direct.count() + d.block.count()
}

func (d *domainLists) swap(other *domainLists) {
	d.proxy.Lock()
	d.proxy.t = other.proxy.t
	d.proxy.Unlock()
	d.direct.Lock()
	d.direct.t = other.direct.t
	d.direct.Unlock()
	d.block.Lock()
	d.block.t = other.block.t
	d.block.Unlock()
}

func loadDomainLists(proxyPath, directPath, blockPath string) (d *domainLists, pN, pS, dN, dS, bN, bS int, err error) {
	if proxyPath == "" && directPath == "" && blockPath == "" {
		return nil, 0, 0, 0, 0, 0, 0, fmt.Errorf("specify proxy_domains, direct_domains and/or block_domains")
	}

	d = newDomainLists()
	if proxyPath != "" {
		pN, pS, err = d.proxy.loadFile(proxyPath)
		if err != nil {
			return nil, 0, 0, 0, 0, 0, 0, fmt.Errorf("proxy domains: %w", err)
		}
	}
	if directPath != "" {
		dN, dS, err = d.direct.loadFile(directPath)
		if err != nil {
			return nil, 0, 0, 0, 0, 0, 0, fmt.Errorf("direct domains: %w", err)
		}
	}
	if blockPath != "" {
		bN, bS, err = d.block.loadFile(blockPath)
		if err != nil {
			return nil, 0, 0, 0, 0, 0, 0, fmt.Errorf("block domains: %w", err)
		}
	}
	return d, pN, pS, dN, dS, bN, bS, nil
}

func (d *domainTree) has(s string) (ok bool) {
	dr := domainReverse(s)

	d.RLock()
	if d.t == nil {
		d.RUnlock()
		return false
	}
	pfx, _, ok := d.t.LongestPrefix(dr)
	d.RUnlock()

	if ok {
		if pfx == dr {
			return true
		}

		if len(dr) > len(pfx) && dr[len(pfx)] != '.' {
			return false
		}

		return true
	}

	return false
}

func (d *domainTree) loadFile(path string) (i, s int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("unable to open file: %w", err)
	}
	defer f.Close()

	domains := []string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		dm := sc.Text()
		if !reDomain.MatchString(dm) {
			s++
			continue
		}

		domains = append(domains, domainReverse(dm))
	}

	if err = sc.Err(); err != nil {
		return 0, 0, fmt.Errorf("unable to read file: %w", err)
	}

	i, ss, err := d.loadList(domains)
	return i, s + ss, err
}

func (d *domainTree) loadList(domains []string) (i, s int, err error) {
	sort.Strings(domains)
	t := radix.New()

	for _, dm := range domains {
		sdm, _, ok := t.LongestPrefix(dm)
		if ok && domainLevel(dm) != domainLevel(sdm) {
			s++
			continue
		}

		t.Insert(dm, true)
		i++
	}

	d.Lock()
	d.t = t
	d.Unlock()
	return
}

func (d *domainTree) count() int {
	d.RLock()
	defer d.RUnlock()
	if d.t == nil {
		return 0
	}
	return d.t.Len()
}

func newDomainTree() (d *domainTree) {
	d = &domainTree{
		t: radix.New(),
	}

	return
}
