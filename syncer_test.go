package main

import (
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_syncer(t *testing.T) {
	e0 := &cacheEntry{
		IP:     net.ParseIP("1.2.3.4"),
		Domain: "foo.bar",
		TS:     time.Now(),
	}

	ga := func() []*cacheEntry {
		return []*cacheEntry{
			e0,
		}
	}

	var e1 *cacheEntry
	ch := make(chan struct{})
	add := func(e *cacheEntry, b bool) bool {
		e1 = e
		close(ch)
		return false
	}

	var err2 error
	cb := func(s string, i int, err error) {
		if err != nil {
			err2 = err
			close(ch)
		}
	}

	port := rand.Intn(60000) + 2000
	s, err := newSyncer(&syncerCfg{
		Listen: fmt.Sprintf("127.0.0.1:%d", port),
		Peers:  []string{fmt.Sprintf("127.0.0.1:%d", port)},
		Token:  "test-token",
	}, ga, add, cb)
	assert.Nil(t, err)

	e2 := &cacheEntry{
		IP:     net.ParseIP("4.3.2.1"),
		Domain: "bar.foo",
		TS:     time.Now(),
	}

	s.broadcast(e2)
	<-ch
	assert.Equal(t, e2.IP, e1.IP)
	assert.Equal(t, e2.Domain, e1.Domain)

	ch = make(chan struct{})
	s.syncAll()
	<-ch

	assert.Equal(t, e0.IP, e1.IP)
	assert.Equal(t, e0.Domain, e1.Domain)
	assert.Nil(t, err2)

	err = s.close()
	assert.Nil(t, err)
}

func Test_syncerRequiresToken(t *testing.T) {
	_, err := newSyncer(&syncerCfg{
		Listen: "127.0.0.1:0",
	}, func() []*cacheEntry { return nil }, func(*cacheEntry, bool) bool { return false }, nil)
	assert.Error(t, err)
}

func Test_syncerUnauthorized(t *testing.T) {
	port := rand.Intn(60000) + 2000
	added := 0
	s, err := newSyncer(&syncerCfg{
		Listen:       fmt.Sprintf("127.0.0.1:%d", port),
		Token:        "secret",
		SyncInterval: "0s",
	}, func() []*cacheEntry { return nil }, func(*cacheEntry, bool) bool {
		added++
		return true
	}, nil)
	assert.Nil(t, err)
	defer s.close()

	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get(url + "/fetch")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.Nil(t, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	req, err := http.NewRequest(http.MethodPut, url+"/put", nil)
	assert.Nil(t, err)
	resp, err = http.DefaultClient.Do(req)
	assert.Nil(t, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, 0, added)
}

func Test_syncerCloseWithoutListen(t *testing.T) {
	s, err := newSyncer(&syncerCfg{
		Token:        "secret",
		Peers:        []string{"127.0.0.1:1"},
		SyncInterval: "0s",
	}, func() []*cacheEntry { return nil }, func(*cacheEntry, bool) bool { return false }, nil)
	assert.Nil(t, err)
	assert.Nil(t, s.s)
	assert.Nil(t, s.close())
}
