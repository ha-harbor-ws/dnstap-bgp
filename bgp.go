package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	api "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	gobgp "github.com/osrg/gobgp/v4/pkg/server"
)

type bgpCfg struct {
	AS          uint32
	RouterID    string
	NextHop     string
	NextHopIPv6 string
	SourceIP    string

	Peers []string
	IPv6  bool

	BlockedCommunities   []string `toml:"blocked_communities"`
	UnblockedCommunities []string `toml:"unblocked_communities"`
}

type communitySet struct {
	standard []uint32
	large    []*bgp.LargeCommunity
}

func (c *communitySet) empty() bool {
	return c == nil || (len(c.standard) == 0 && len(c.large) == 0)
}

func (c *communitySet) String() string {
	if c.empty() {
		return "(none)"
	}
	parts := make([]string, 0, len(c.standard)+len(c.large))
	for _, v := range c.standard {
		parts = append(parts, fmt.Sprintf("%d:%d", v>>16, v&0xffff))
	}
	for _, lc := range c.large {
		parts = append(parts, lc.String())
	}
	return strings.Join(parts, " ")
}

func (c *communitySet) attrs() []bgp.PathAttributeInterface {
	if c.empty() {
		return nil
	}
	var attrs []bgp.PathAttributeInterface
	if len(c.standard) > 0 {
		attrs = append(attrs, bgp.NewPathAttributeCommunities(append([]uint32(nil), c.standard...)))
	}
	if len(c.large) > 0 {
		attrs = append(attrs, bgp.NewPathAttributeLargeCommunities(append([]*bgp.LargeCommunity(nil), c.large...)))
	}
	return attrs
}

func parseCommunityList(ss []string) (*communitySet, error) {
	cs := &communitySet{}
	for _, raw := range ss {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if v, ok := bgp.WellKnownCommunityValueMap[strings.ToLower(s)]; ok {
			cs.standard = append(cs.standard, uint32(v))
			continue
		}
		parts := strings.Split(s, ":")
		switch len(parts) {
		case 2:
			a, err1 := strconv.ParseUint(parts[0], 10, 16)
			b, err2 := strconv.ParseUint(parts[1], 10, 16)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid community %q (want ASN:VALUE)", s)
			}
			cs.standard = append(cs.standard, uint32(a)<<16|uint32(b))
		case 3:
			lc, err := bgp.ParseLargeCommunity(s)
			if err != nil {
				return nil, fmt.Errorf("invalid large community %q (want ASN:A:B)", s)
			}
			cs.large = append(cs.large, lc)
		default:
			return nil, fmt.Errorf("invalid community %q (want ASN:VALUE, ASN:A:B, or well-known name)", s)
		}
	}
	return cs, nil
}

type bgpServer struct {
	s *gobgp.BgpServer
	c *bgpCfg

	blockedComm   *communitySet
	unblockedComm *communitySet
}

func newBgp(c *bgpCfg) (b *bgpServer, err error) {
	if c.AS == 0 {
		return nil, fmt.Errorf("you need to provide AS")
	}

	if len(c.Peers) == 0 {
		return nil, fmt.Errorf("you need to provide at least one peer")
	}

	blockedComm, err := parseCommunityList(c.BlockedCommunities)
	if err != nil {
		return nil, fmt.Errorf("blocked_communities: %w", err)
	}
	unblockedComm, err := parseCommunityList(c.UnblockedCommunities)
	if err != nil {
		return nil, fmt.Errorf("unblocked_communities: %w", err)
	}

	b = &bgpServer{
		s:             gobgp.NewBgpServer(),
		c:             c,
		blockedComm:   blockedComm,
		unblockedComm: unblockedComm,
	}
	go b.s.Serve()

	if err = b.s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			Asn:        c.AS,
			RouterId:   c.RouterID,
			ListenPort: -1,
		},
	}); err != nil {
		return
	}

	for _, p := range c.Peers {
		if err = b.addPeer(p); err != nil {
			return
		}
	}

	return
}

func (b *bgpServer) addPeer(addr string) (err error) {
	port := 179

	if t := strings.SplitN(addr, ":", 2); len(t) == 2 {
		addr = t[0]

		if port, err = strconv.Atoi(t[1]); err != nil {
			return fmt.Errorf("unable to parse port '%s' as int: %s", t[1], err)
		}
	}

	p := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: addr,
			PeerAsn:         b.c.AS,
		},

		AfiSafis: []*api.AfiSafi{
			{
				Config: &api.AfiSafiConfig{
					Family: &api.Family{
						Afi:  api.Family_AFI_IP,
						Safi: api.Family_SAFI_UNICAST,
					},
					Enabled: true,
				},
			},
			{
				Config: &api.AfiSafiConfig{
					Family: &api.Family{
						Afi:  api.Family_AFI_IP6,
						Safi: api.Family_SAFI_UNICAST,
					},
					Enabled: true,
				},
			},
		},

		Timers: &api.Timers{
			Config: &api.TimersConfig{
				ConnectRetry: 10,
			},
		},

		Transport: &api.Transport{
			MtuDiscovery:  true,
			RemoteAddress: addr,
			RemotePort:    uint32(port),
		},
	}

	if b.c.SourceIP != "" {
		p.Transport.LocalAddress = b.c.SourceIP
	}

	return b.s.AddPeer(context.Background(), &api.AddPeerRequest{
		Peer: p,
	})
}

func prefixFromIP(ip net.IP) (netip.Prefix, bgp.Family, error) {
	if v4 := ip.To4(); v4 != nil {
		addr, ok := netip.AddrFromSlice(v4)
		if !ok {
			return netip.Prefix{}, 0, fmt.Errorf("invalid IPv4 address: %s", ip)
		}
		return netip.PrefixFrom(addr, 32), bgp.RF_IPv4_UC, nil
	}

	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Prefix{}, 0, fmt.Errorf("invalid IP address: %s", ip)
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return netip.PrefixFrom(addr, 32), bgp.RF_IPv4_UC, nil
	}
	return netip.PrefixFrom(addr, 128), bgp.RF_IPv6_UC, nil
}

func (b *bgpServer) nextHop(family bgp.Family) (netip.Addr, error) {
	var raw string
	if family == bgp.RF_IPv6_UC {
		if b.c.NextHopIPv6 != "" {
			raw = b.c.NextHopIPv6
		} else {
			raw = "fd00::1"
		}
	} else if b.c.NextHop != "" {
		raw = b.c.NextHop
	} else if b.c.SourceIP != "" {
		raw = b.c.SourceIP
	} else {
		raw = b.c.RouterID
	}

	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid next-hop %q: %w", raw, err)
	}
	return addr, nil
}

func (b *bgpServer) communitiesFor(class domainClass) *communitySet {
	switch class {
	case domainBlocked:
		return b.blockedComm
	case domainUnblocked:
		return b.unblockedComm
	default:
		return nil
	}
}

func (b *bgpServer) getPath(ip net.IP, class domainClass) (*apiutil.Path, error) {
	prefix, family, err := prefixFromIP(ip)
	if err != nil {
		return nil, err
	}
	if family == bgp.RF_IPv6_UC && !b.c.IPv6 {
		return nil, nil
	}

	nlri, err := bgp.NewIPAddrPrefix(prefix)
	if err != nil {
		return nil, err
	}

	nh, err := b.nextHop(family)
	if err != nil {
		return nil, err
	}

	origin := bgp.NewPathAttributeOrigin(0)
	aspath := bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{
		bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, []uint32{}),
	})
	attrs := []bgp.PathAttributeInterface{origin, aspath}

	if family == bgp.RF_IPv6_UC {
		mp, err := bgp.NewPathAttributeMpReachNLRI(family, []bgp.PathNLRI{{NLRI: nlri}}, nh)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, mp)
	} else {
		nextHop, err := bgp.NewPathAttributeNextHop(nh)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, nextHop)
	}

	attrs = append(attrs, b.communitiesFor(class).attrs()...)

	return &apiutil.Path{
		Family: family,
		Nlri:   nlri,
		Attrs:  attrs,
	}, nil
}

func (b *bgpServer) addHost(ip net.IP, class domainClass) error {
	p, err := b.getPath(ip, class)
	if err != nil || p == nil {
		return err
	}

	_, err = b.s.AddPath(apiutil.AddPathRequest{
		Paths: []*apiutil.Path{p},
	})
	return err
}

func (b *bgpServer) delHost(ip net.IP, class domainClass) error {
	p, err := b.getPath(ip, class)
	if err != nil || p == nil {
		return err
	}

	return b.s.DeletePath(apiutil.DeletePathRequest{
		Paths: []*apiutil.Path{p},
	})
}

func (b *bgpServer) close() error {
	ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
	defer cf()
	return b.s.StopBgp(ctx, &api.StopBgpRequest{})
}
