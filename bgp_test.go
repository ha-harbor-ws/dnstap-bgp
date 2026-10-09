package main

import (
	"net"
	"testing"

	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/stretchr/testify/assert"
)

func Test_BGP(t *testing.T) {
	b, err := newBgp(&bgpCfg{
		AS:       65000,
		RouterID: "127.0.0.1",
		Peers: []string{
			"127.0.0.1",
		},
		BlockedCommunities:   []string{"65000:666"},
		UnblockedCommunities: []string{"65000:100"},
	})

	assert.Nil(t, err)
	err = b.addHost(net.ParseIP("1.2.3.4"), domainBlocked)
	assert.Nil(t, err)

	err = b.delHost(net.ParseIP("1.2.3.4"), domainBlocked)
	assert.Nil(t, err)

	err = b.close()
	assert.Nil(t, err)
}

func Test_parseCommunityList(t *testing.T) {
	cs, err := parseCommunityList([]string{"65000:666", "no-export", "65000:1:2"})
	assert.Nil(t, err)
	assert.Equal(t, uint32(65000)<<16|666, cs.standard[0])
	assert.Equal(t, uint32(bgp.COMMUNITY_NO_EXPORT), cs.standard[1])
	assert.Equal(t, uint32(65000), cs.large[0].ASN)
	assert.Equal(t, uint32(1), cs.large[0].LocalData1)
	assert.Equal(t, uint32(2), cs.large[0].LocalData2)

	_, err = parseCommunityList([]string{"not-a-community"})
	assert.Error(t, err)
}

func Test_getPathCommunities(t *testing.T) {
	blocked, err := parseCommunityList([]string{"65000:666"})
	assert.Nil(t, err)
	unblocked, err := parseCommunityList([]string{"65000:100"})
	assert.Nil(t, err)

	b := &bgpServer{
		c:             &bgpCfg{RouterID: "192.0.2.1", NextHop: "192.0.2.1"},
		blockedComm:   blocked,
		unblockedComm: unblocked,
	}

	p, err := b.getPath(net.ParseIP("192.0.2.8"), domainBlocked)
	assert.Nil(t, err)

	var found []uint32
	for _, a := range p.Attrs {
		if c, ok := a.(*bgp.PathAttributeCommunities); ok {
			found = c.Value
		}
	}
	assert.Equal(t, []uint32{uint32(65000)<<16 | 666}, found)

	p, err = b.getPath(net.ParseIP("192.0.2.8"), domainUnblocked)
	assert.Nil(t, err)
	found = nil
	for _, a := range p.Attrs {
		if c, ok := a.(*bgp.PathAttributeCommunities); ok {
			found = c.Value
		}
	}
	assert.Equal(t, []uint32{uint32(65000)<<16 | 100}, found)
}
