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
	domainBlocked   domainClass = "blocked"
	domainUnblocked domainClass = "unblocked"
)

type domainLists struct {
	blocked   *domainTree
	unblocked *domainTree
}

func newDomainLists() *domainLists {
	return &domainLists{
		blocked:   newDomainTree(),
		unblocked: newDomainTree(),
	}
}

func (d *domainLists) classOf(s string) (domainClass, bool) {
	if d.blocked.has(s) {
		return domainBlocked, true
	}
	if d.unblocked.has(s) {
		return domainUnblocked, true
	}
	return "", false
}

func (d *domainLists) has(s string) bool {
	_, ok := d.classOf(s)
	return ok
}

func (d *domainLists) count() int {
	return d.blocked.count() + d.unblocked.count()
}

func (d *domainLists) swap(other *domainLists) {
	d.blocked.Lock()
	d.blocked.t = other.blocked.t
	d.blocked.Unlock()
	d.unblocked.Lock()
	d.unblocked.t = other.unblocked.t
	d.unblocked.Unlock()
}

func loadDomainLists(blockedPath, unblockedPath string) (d *domainLists, bN, bS, uN, uS int, err error) {
	if blockedPath == "" && unblockedPath == "" {
		return nil, 0, 0, 0, 0, fmt.Errorf("specify blocked_domains and/or unblocked_domains")
	}

	d = newDomainLists()
	if blockedPath != "" {
		bN, bS, err = d.blocked.loadFile(blockedPath)
		if err != nil {
			return nil, 0, 0, 0, 0, fmt.Errorf("blocked domains: %w", err)
		}
	}
	if unblockedPath != "" {
		uN, uS, err = d.unblocked.loadFile(unblockedPath)
		if err != nil {
			return nil, 0, 0, 0, 0, fmt.Errorf("unblocked domains: %w", err)
		}
	}
	return d, bN, bS, uN, uS, nil
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
