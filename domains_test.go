package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_domainReverse(t *testing.T) {
	d := map[string]string{
		"a.b.c.d.e.f.g":    "g.f.e.d.c.b.a",
		"api.facebook.com": "com.facebook.api",
	}

	for k, v := range d {
		r := domainReverse(k)
		assert.Equal(t, r, v)
	}
}

func Test_domainTree(t *testing.T) {
	l := []string{
		"com.facebook",
	}

	dt := newDomainTree()
	dt.loadList(l)
	assert.True(t, dt.has("api.facebook.com"))
	assert.False(t, dt.has("facebookk.com"))

	testlist := "__test.txt"
	err := os.WriteFile(testlist, []byte("foo.bar\n123123----foo\n"), 0666)
	assert.Nil(t, err)

	i, s, err := dt.loadFile(testlist)
	assert.Nil(t, err)
	assert.Equal(t, 1, i)
	assert.Equal(t, 1, s)

	os.Remove(testlist)
}

func Test_loadFileCloses(t *testing.T) {
	testlist := "__test_close.txt"
	err := os.WriteFile(testlist, []byte("foo.bar\n"), 0666)
	assert.Nil(t, err)

	dt := newDomainTree()
	i, s, err := dt.loadFile(testlist)
	assert.Nil(t, err)
	assert.Equal(t, 1, i)
	assert.Equal(t, 0, s)

	err = os.Remove(testlist)
	assert.Nil(t, err)
}

func Test_domainTreeSub(t *testing.T) {
	l := []string{
		"com.x",
	}

	dt := newDomainTree()
	dt.loadList(l)
	assert.True(t, dt.has("x.com"))
	assert.True(t, dt.has("something.x.com"))
	assert.False(t, dt.has("something.xiaomi.com"))
	assert.False(t, dt.has("xiaomi.com"))
}

func Test_domainLevel(t *testing.T) {
	assert.Equal(t, 5, domainLevel("a.b.c.d.e"))
}
