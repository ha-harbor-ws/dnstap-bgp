package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	syncerAuthScheme = "Bearer "
	syncerMaxPutBody = 1 << 20
)

type syncerCfg struct {
	Listen                string
	SyncInterval          string
	Peers                 []string
	Token                 string
	TLSCert               string
	TLSKey                string
	TLSInsecureSkipVerify bool
}

type getAllFunc func() []*cacheEntry
type addFunc func(*cacheEntry, bool) bool
type syncFunc func(string, int, error)

type syncer struct {
	s *http.Server
	c *http.Client

	syncInterval time.Duration
	peers        []string
	token        string
	useTLS       bool

	getAll getAllFunc
	add    addFunc
	syncCb syncFunc

	shutdown  chan struct{}
	closeOnce sync.Once
}

func newSyncer(cf *syncerCfg, getAll getAllFunc, add addFunc, syncCb syncFunc) (s *syncer, err error) {
	if cf.Token == "" {
		return nil, fmt.Errorf("syncer token is required")
	}

	if (cf.TLSCert == "") != (cf.TLSKey == "") {
		return nil, fmt.Errorf("tlsCert and tlsKey must be set together")
	}

	s = &syncer{
		getAll:       getAll,
		add:          add,
		peers:        cf.Peers,
		token:        cf.Token,
		useTLS:       cf.TLSCert != "" && cf.TLSKey != "",
		syncCb:       syncCb,
		shutdown:     make(chan struct{}),
		syncInterval: 10 * time.Minute,
	}

	if cf.SyncInterval != "" {
		if s.syncInterval, err = time.ParseDuration(cf.SyncInterval); err != nil {
			return nil, fmt.Errorf("unable to parse syncInterval: %w", err)
		}
	}

	if len(cf.Peers) > 0 {
		tr := http.DefaultTransport
		if s.useTLS {
			tr = &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion:         tls.VersionTLS12,
					InsecureSkipVerify: cf.TLSInsecureSkipVerify,
				},
			}
		}
		s.c = &http.Client{
			Timeout:   5 * time.Second,
			Transport: tr,
		}
	}

	if cf.Listen != "" {
		addr, err := net.ResolveTCPAddr("tcp", cf.Listen)
		if err != nil {
			return nil, err
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return nil, err
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/fetch", s.handleFetch)
		mux.HandleFunc("/put", s.handlePut)

		s.s = &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}

		go func() {
			var serveErr error
			if s.useTLS {
				serveErr = s.s.ServeTLS(l, cf.TLSCert, cf.TLSKey)
			} else {
				serveErr = s.s.Serve(l)
			}
			if serveErr != nil && serveErr != http.ErrServerClosed {
				log.Printf("Syncer: server error: %s", serveErr)
			}
		}()
	}

	if s.syncInterval > 0 && len(s.peers) > 0 {
		go s.syncScheduler()
	}

	return
}

func (s *syncer) authorized(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	if len(got) < len(syncerAuthScheme) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(got[:len(syncerAuthScheme)]), []byte(syncerAuthScheme)) != 1 {
		return false
	}
	provided := got[len(syncerAuthScheme):]
	if len(provided) != len(s.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
}

func (s *syncer) handleFetch(wr http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		wr.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		wr.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(s.getAll())
}

func (s *syncer) handlePut(wr http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		wr.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPut {
		wr.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()
	body := http.MaxBytesReader(wr, r.Body, syncerMaxPutBody)

	c := &cacheEntry{}
	if err := json.NewDecoder(body).Decode(c); err != nil && err != io.EOF {
		wr.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(wr, "Bad request: %s", err)
		return
	}
	if len(c.IP) == 0 || c.Domain == "" {
		wr.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(wr, "Bad request: ip and domain are required")
		return
	}

	s.add(c, false)
}

func (s *syncer) broadcast(e *cacheEntry) (err error) {
	if s.c == nil {
		return
	}

	for _, p := range s.peers {
		if err = s.send(e, p); err != nil {
			return
		}
	}

	return
}

func (s *syncer) scheme() string {
	if s.useTLS {
		return "https"
	}
	return "http"
}

func (s *syncer) callPeer(p, handler, method string, body io.Reader) (resp *http.Response, err error) {
	u := fmt.Sprintf("%s://%s/%s", s.scheme(), p, handler)
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", syncerAuthScheme+s.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if resp, err = s.c.Do(req); err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP Code not 200: %d", resp.StatusCode)
	}

	return
}

func (s *syncer) syncScheduler() {
	t := time.NewTicker(s.syncInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			s.syncAll()

		case <-s.shutdown:
			return
		}
	}
}

func (s *syncer) syncAll() {
	for _, p := range s.peers {
		new := 0

		es, err := s.fetchRemote(p)
		if err != nil {
			s.syncCb(p, 0, err)
			continue
		}

		for _, e := range es {
			if s.add(e, false) {
				new++
			}
		}

		log.Printf("Syncer: got %d (%d new) entries from peer %s", len(es), new, p)

		s.syncCb(p, new, nil)
	}
}

func (s *syncer) fetchRemote(p string) (es []*cacheEntry, err error) {
	resp, err := s.callPeer(p, "fetch", http.MethodGet, nil)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if err = json.NewDecoder(resp.Body).Decode(&es); err != nil {
		return
	}

	return
}

func (s *syncer) send(e *cacheEntry, p string) (err error) {
	js, err := json.Marshal(e)
	if err != nil {
		return err
	}
	resp, err := s.callPeer(p, "put", http.MethodPut, bytes.NewReader(js))
	if err != nil {
		return
	}

	resp.Body.Close()
	return
}

func (s *syncer) close() error {
	s.closeOnce.Do(func() {
		close(s.shutdown)
	})

	if s.s == nil {
		return nil
	}

	c, f := context.WithTimeout(context.Background(), 5*time.Second)
	defer f()

	return s.s.Shutdown(c)
}
