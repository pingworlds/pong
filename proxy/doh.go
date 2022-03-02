package proxy

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"golang.org/x/net/http2"

	"github.com/google/uuid"
	"github.com/pingworlds/pong/config"
	"github.com/pingworlds/pong/xnet"
)

var cfg = config.Config

var locDohs []*DohClient
var rmtDohs []*DohClient

func InitDoh() {
	locDohs = []*DohClient{}
	rmtDohs = []*DohClient{}

	for _, doh := range cfg.WorkDohs {
		if doh.Disabled {
			continue
		}
		if client, err := NewDohClient(doh); err == nil {
			locDohs = append(locDohs, client)
			rmtDohs = append(rmtDohs, client)
		}
	}
	if cfg.LocalDohMode {
		go func() {
			CheckDohClients(true, locDohs, "apple.com")
			SortDohClients(locDohs)
		}()
	}
	if cfg.RemoteDohMode {
		go func() {
			CheckDohClients(true, rmtDohs, "google.com")
			SortDohClients(rmtDohs)
		}()
	}
}

func SortDohClients(clients []*DohClient) {
	sort.Slice(clients, func(i, j int) bool {
		return clients[i].Latency < clients[j].Latency
	})
}

func DohQuery(b []byte, isDirect bool) (p []byte, err error) {
	clients := rmtDohs
	if isDirect {
		clients = locDohs
	}
	for _, client := range clients {
		if p, err = client.Query(b, isDirect); err == nil {
			return
		}
	}
	return nil, fmt.Errorf("no doh client")
}

type DohClient struct {
	*xnet.Doh
	Latency int
	addr    xnet.Addr
	url     *url.URL
	conn    *http2.ClientConn
	cfg     *tls.Config
	h       http.Header 
}

func (d *DohClient) Query(b []byte, isDirect bool) (p []byte, err error) {
	var req *http.Request
	if req, err = d.NewGetRequest(b); err != nil {
		return
	}

	var rsp *http.Response
	if rsp, err = d.Do(req, isDirect); err != nil {
		d.Close()
		return
	}
	defer rsp.Body.Close()
	code := rsp.StatusCode
	if code < 200 || code > 299 {
		err = fmt.Errorf("doh response error, status code %d", code)
		return
	}
	p, err = io.ReadAll(rsp.Body)
	return
}

//always http2
func (d *DohClient) Do(req *http.Request, isDirect bool) (rsp *http.Response, err error) {
	tr := http2.Transport{}
	if d.conn == nil {
		var tc *tls.Conn
		tc, err = d.dialDoh(isDirect)
		if err != nil {
			return
		}
		if d.conn, err = tr.NewClientConn(tc); err != nil {
			tc.Close()
			return
		}
	}
	return d.conn.RoundTrip(req)
}

func (d DohClient) dialDoh(isDirect bool) (tc *tls.Conn, err error) {
	var f Filter
	tun := locCtrl.NewTunnel(uuid.NewString(), nil, CONNECT, d.addr)
	defer tun.Close()
	if isDirect {
		f = EmptyFilter
		err = directPeer.Open(tun)
	} else {
		f, err = locCtrl.Open(tun)
	}

	if err != nil {
		return
	}

	f.BeforeSend(tun)
	tc = tls.Client(&rawConn{Conn: tun.Dst, filter: f, tun: tun}, d.cfg)
	if err = tc.Handshake(); err != nil {
		err = fmt.Errorf("doh tls handshake error %v", err)
		tun.Dst.Close()
	}
	return
}

func (d DohClient) NewPostRequest(b []byte) (req *http.Request, err error) {
	req = &http.Request{
		Method: http.MethodPost,
		URL:    d.url,
		Body:   io.NopCloser(bytes.NewReader(b)),
		Header: d.h,
	}
	return
}

func (d DohClient) NewGetRequest(b []byte) (req *http.Request, err error) {
	var u *url.URL
	b64 := base64.RawURLEncoding.EncodeToString(b)
	s := fmt.Sprintf("%s?dns=%s", d.Path, b64)
	if u, err = url.ParseRequestURI(s); err != nil {
		err = fmt.Errorf("invalid doh request url[%s], %v", s, err)
		return
	}
	req = &http.Request{
		Method: http.MethodGet,
		URL:    u,
		Header: d.h,
	}
	return
}

func (d DohClient) Close() (err error) {
	if d.conn != nil {
		return d.conn.Close()
	}
	return
}

func NewDohClient(doh *xnet.Doh) (client *DohClient, err error) {
	var u *url.URL
	u, err = url.ParseRequestURI(doh.Path)
	if err != nil {
		err = fmt.Errorf("invalid doh url[%s],  %v", doh.Path, err)
		return
	}

	addrBytes := []byte(u.Host)
	n := len(addrBytes)
	port := 443
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			port = 443
		}
	}
	b := make([]byte, 4+n)
	b[0] = xnet.DOMAIN
	b[1] = byte(n)
	copy(b[2:2+n], addrBytes)
	b[n+2] = byte(port >> 8)
	b[n+3] = byte(port & 0xFF)

	addr := xnet.Addr(b)
	h := http.Header{}
	h.Set("Content-Type", "application/dns-message")

	client = &DohClient{
		Doh:  doh,
		url:  u,
		addr: addr,
		h:    h,
		cfg:  &tls.Config{InsecureSkipVerify: true, ServerName: u.Hostname(), NextProtos: []string{"h2"}},
	}

	return
}

type rawConn struct {
	net.Conn
	filter    Filter
	handshake bool
	tun       *Tunnel
}

//vless/socks need skip response header
func (c *rawConn) Read(b []byte) (n int, err error) {
	if !c.handshake {
		c.handshake = true
		c.filter.BeforeReceive(c.tun)
	}
	return c.Conn.Read(b)
}
