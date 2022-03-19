package transport

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/pingworlds/pong/xnet"
)

type Dialer interface {
	Dial(p *xnet.Point) (conn net.Conn, err error)
}

type Handle func(conn net.Conn)

type Handler interface {
	Handle(conn net.Conn)
}

type HandleUDP func(conn *net.UDPConn, b []byte, addr *net.UDPAddr) (err error)

type UDPHandler interface {
	HandleUDP(conn net.UDPConn, b []byte, addr *net.UDPAddr) (err error)
}

type Server interface {
	Listen(p *xnet.Point, handle Handle) (err error)
	Close() error
}

type CloserServer struct {
	io.Closer
}

func (s CloserServer) Close() error {
	if s.Closer != nil {
		return s.Closer.Close()
	}
	return nil
}

func (s CloserServer) Listen(p *xnet.Point, handle Handle) (err error) {
	return xnet.Err_NotImplement
}

type ServerFn func() (srv Server)

type DialerFn func(point *xnet.Point) (d Dialer)

type DialFn func(point *xnet.Point) (conn net.Conn, err error)

func Dial(point *xnet.Point) (conn net.Conn, err error) {
	dial := dialfns[point.Transport]
	if dial == nil {
		return nil, fmt.Errorf("transport protocol %s not support", point.Transport)
	}
	return dial(point)
}

func NewServer(transport string) (svr Server, err error) {
	fn := svrfns[transport]
	if fn == nil {
		err = fmt.Errorf("transport protocol %s not support", transport)
		return
	}
	svr = fn()
	return
}

var mu sync.RWMutex
var svrfns = map[string]ServerFn{}
var dialfns = make(map[string]DialFn)

func RegServerFn(name string, fn ServerFn) error {
	mu.Lock()
	defer mu.Unlock()
	svrfns[name] = fn
	return nil
}

func RegDialFn(name string, fn DialFn) error {
	mu.Lock()
	defer mu.Unlock()
	dialfns[name] = fn

	return nil
}

var TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: false}

var TLSSkipConfig = &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}

type TakerFn func(p xnet.Point) Taker

type Taker interface {
	GetTLSConfig() *tls.Config
	NewClient(cfg *tls.Config) *http.Client
	NewRequest(u *url.URL) *http.Request
	GetSchema() string
	NewURL(schema string) *url.URL
	Do(req *http.Request, cfg *Config) (*http.Response, error)
}

type Config struct {
	Taker
	TLSConfig *tls.Config
	Schema    string
	URL       *url.URL
	Client    *http.Client
	Error     error
}

func NewConfig(p *xnet.Point, t Taker) *Config {
	cfg := &Config{Taker: t}
	cfg.TLSConfig = cfg.GetTLSConfig()
	cfg.Schema = cfg.GetSchema()
	cfg.URL = cfg.NewURL(cfg.Schema)
	cfg.Client = cfg.NewClient(cfg.TLSConfig)
	return cfg
}

type configCache struct {
	cfgs map[string]*Config
	mu   sync.Mutex
}

func (c *configCache) Cache(id string, cfg *Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfgs[id] = cfg
}

func (c *configCache) Get(id string) *Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfgs[id]
}

func (c *configCache) Remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg := c.cfgs[id]
	if cfg != nil {
		cfg.Client.CloseIdleConnections()
	}
	delete(c.cfgs, id)
}

var cache = &configCache{cfgs: make(map[string]*Config)}

func GetConfig(id string) (cfg *Config) {
	return cache.Get(id)
}

func CacheConfig(id string, cfg *Config) {
	cache.Cache(id, cfg)
}

func RemoveConfig(id string) {
	cache.Remove(id)
}

type HttpTaker xnet.Point

func (t *HttpTaker) GetSchema() string {
	return t.Transport
}

func (t HttpTaker) GetTLSConfig() *tls.Config {
	cfg := TLSConfig
	if t.Sni == "" {
		if t.InsecureSkip {
			cfg = TLSSkipConfig
		}
	} else {
		cfg = &tls.Config{ServerName: t.Sni, InsecureSkipVerify: t.InsecureSkip}
	}
	return cfg
}

func (t HttpTaker) NewRequest(u *url.URL) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, u.String(), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	return req
}

func (t HttpTaker) NewURL(schema string) *url.URL {
	return &url.URL{
		Scheme: schema,
		Host:   t.Host,
		Path:   t.Path,
	}
}
func (t HttpTaker) Do(req *http.Request, cfg *Config) (*http.Response, error) {
	return cfg.Client.Do(req)
}

func (t HttpTaker) NewClient(cfg *tls.Config) *http.Client {
	return http.DefaultClient
}

func NewHttpTaker(p xnet.Point) *HttpTaker {
	t := HttpTaker(p)
	return &t
}
func NewTaker(p xnet.Point) Taker {
	return NewHttpTaker(p)
}

func NewDefaultHttpDialer(fn TakerFn) *defaultHttpDialer {
	return &defaultHttpDialer{NewTaker: fn}
}

type HttpDialer interface {
	GetConfig(p *xnet.Point) *Config
	Dial(p *xnet.Point) (conn net.Conn, err error)
}

type defaultHttpDialer struct {
	NewTaker TakerFn
}

func (d defaultHttpDialer) GetConfig(p *xnet.Point) *Config {
	cfg := GetConfig(p.ID())
	if cfg == nil {
		t := d.NewTaker(*p)
		cfg = NewConfig(p, t)
		CacheConfig(p.ID(), cfg)
	}
	return cfg
}

func (d defaultHttpDialer) Dial(p *xnet.Point) (conn net.Conn, err error) {
	cfg := d.GetConfig(p)
	req := cfg.NewRequest(cfg.URL)
	pr, pw := io.Pipe()
	req.Body = pr
	var rsp *http.Response
	rsp, err = cfg.Do(req, cfg)
	if err != nil {
		log.Println(err)
		return
	}
	if rsp.StatusCode < 200 || rsp.StatusCode > 299 {
		rsp.Body.Close()
		err = fmt.Errorf("http response error status code %d", rsp.StatusCode)
		RemoveConfig(p.ID())
		return
	}
	c := NewClientConn(pr, pw, rsp.Body, cfg.Client)
	conn = c
	return
}

func NewClientConn(pr *io.PipeReader, pw *io.PipeWriter, r io.ReadCloser, client *http.Client) *ClientConn {
	return &ClientConn{pr: pr, pw: pw, r: r, client: client}
}

type ClientConn struct {
	net.Conn
	pr     *io.PipeReader
	pw     *io.PipeWriter
	r      io.ReadCloser
	client *http.Client
}

func (c ClientConn) Write(b []byte) (int, error) {
	return c.pw.Write(b)
}

func (c ClientConn) Read(b []byte) (int, error) {
	// return xnet.ReadWithTimeout(c.r, b, 300000)
	return c.r.Read(b)
}

func (c ClientConn) Close() error {
	// c.client.CloseIdleConnections()
	c.pw.Close()
	c.pr.Close()
	return c.r.Close()
}

func (c ClientConn) SetDeadline(t time.Time) error {
	return nil
}

func (c ClientConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c ClientConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type ServeConn struct {
	net.Conn
	r io.ReadCloser
	w io.Writer
}

func (s ServeConn) Write(b []byte) (n int, err error) {
	if n, err = s.w.Write(b); err == nil {
		err = s.flush()
	}
	return
}

func (s ServeConn) Read(b []byte) (int, error) {
	return s.r.Read(b)
}

func (s ServeConn) Close() error {
	return s.r.Close()
}

func (s ServeConn) flush() error {
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	} else {
		return fmt.Errorf("connection error,flush is nil")
	}
	return nil
}

func (s ServeConn) SetDeadline(t time.Time) error {
	return nil
}

func (s ServeConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (s ServeConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func NewServeConn(w http.ResponseWriter, r *http.Request) (conn net.Conn, err error) {
	sc := &ServeConn{r: r.Body, w: w}
	w.WriteHeader(200)
	if err = sc.flush(); err == nil {
		conn = sc
	}
	return
}
