package h2

import (
	"crypto/tls"
	"log"
	"net"
	"net/http"

	"golang.org/x/net/http2"

	"github.com/pingworlds/pong/xnet"
	"github.com/pingworlds/pong/xnet/transport"
	"github.com/pingworlds/pong/xnet/transport/tcp"
)

type Taker struct {
	*transport.HttpTaker
}

func (t Taker) GetSchema() string {
	s := "https"
	if t.Transport == "h2c" {
		s = "http"
	}
	return s
}

func (t Taker) NewClient(cfg *tls.Config) (c *http.Client) {
	if t.Transport == "h2c" {
		c = &http.Client{Transport: &http2.Transport{
			DisableCompression: true,
			AllowHTTP:          true,
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				return xnet.Dialer.Dial(network, addr)
			},
		}}
	} else {
		c = &http.Client{
			Transport: &http2.Transport{
				DisableCompression: true, //likely required
				TLSClientConfig:    cfg,
				AllowHTTP:          false,
				DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
					return tls.DialWithDialer(xnet.Dialer, network, addr, cfg)
				},
			}}
	}
	return
}

func NewTaker(p xnet.Point) transport.Taker {
	return &Taker{HttpTaker: transport.NewHttpTaker(p)}
}

func NewDialer() transport.Dialer {
	return transport.NewDefaultHttpDialer(NewTaker)
}

type server struct {
	transport.CloserServer
}

func (s *server) Listen(p *xnet.Point, handle transport.Handle) (err error) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.Path != "" && r.URL.Path != p.Path {
			log.Printf("deny illegal access path %s\n", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		src, err := transport.NewServeConn(w, r)
		if err != nil {
			log.Printf("build ServeConn failed %s\n", r.URL.Path)
			return
		}
		defer src.Close()
		handle(src)
	})

	svr := &http.Server{
		Addr:    p.Host,
		Handler: h,
	}
	h2svr := &http2.Server{}
	if err = http2.ConfigureServer(svr, h2svr); err != nil {
		return
	}

	if p.Transport == "h2" {
		s.Closer = svr
		err = svr.ListenAndServeTLS(p.CertFile, p.KeyFile)
	} else {
		tsvr := tcp.NewServer()
		s.Closer = tsvr
		err = tsvr.Listen(p, func(conn net.Conn) {
			h2svr.ServeConn(conn, &http2.ServeConnOpts{BaseConfig: svr})
		})
	}
	return
}

func NewServer() transport.Server {
	return &server{}
}

var dialer = NewDialer()

func init() {
	transport.RegServerFn("h2", NewServer)
	transport.RegDialFn("h2", dialer.Dial)

	transport.RegServerFn("h2c", NewServer)
	transport.RegDialFn("h2c", dialer.Dial)
}
