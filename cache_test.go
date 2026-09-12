package main

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_CachePurgeUnmatched(t *testing.T) {
	vanished := 0
	cb := func(e *cacheEntry) {
		vanished++
	}

	c := newCache(time.Hour, nil)
	c.add(&cacheEntry{
		IP:     net.ParseIP("1.1.1.1"),
		Domain: "keep.example",
		TS:     time.Now(),
	})
	c.add(&cacheEntry{
		IP:     net.ParseIP("2.2.2.2"),
		Domain: "drop.example",
		TS:     time.Now(),
	})

	dt := newDomainTree()
	_, _, err := dt.loadList([]string{"example.keep"})
	assert.Nil(t, err)

	n := c.purgeUnmatched(dt, cb)
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, vanished)
	assert.True(t, c.exists(net.ParseIP("1.1.1.1"), false))
	assert.False(t, c.exists(net.ParseIP("2.2.2.2"), false))
}

func Test_Cache(t *testing.T) {
	expired := 0
	cb := func(e *cacheEntry) {
		expired++
	}

	e := &cacheEntry{
		IP:     net.ParseIP("1.2.3.4"),
		Domain: "test.foo",
		TS:     time.Now(),
	}

	c := newCache(time.Millisecond, cb)
	c.add(e)
	assert.True(t, c.exists(e.IP, false))
	assert.Equal(t, 1, c.count())
	ee := c.getAll()
	assert.Equal(t, e, ee[0])
	time.Sleep(2 * time.Millisecond)
	c.cleanup()
	assert.Equal(t, 0, c.count())
	assert.Equal(t, 1, expired)
}
