package h1

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/pingworlds/pong/xnet"
	"github.com/pingworlds/pong/xnet/transport"
	"github.com/pingworlds/pong/xnet/transport/tcp"
)

type Dialer struct {
	transport.HttpDialer
}

func (d Dialer) Dial(p xnet.Point) (conn net.Conn, err error) {
	cfg := d.GetConfig(p)
	var c net.Conn
	if c, err = tcp.DialTCP(p); err != nil {
		return
	}
	conn = c
	if p.Transport == "https" {
		tc := tls.Client(c, cfg.TLSConfig)
		if err = tc.Handshake(); err != nil {
			c.Close()
			return
		}
		conn = tc
	}

	req := cfg.NewRequest(cfg.URL)  
	if err = req.Write(conn); err != nil {
		c.Close()
		return
	}
	br := bufio.NewReader(conn)
	var rsp *http.Response
	if rsp, err = http.ReadResponse(br, req); err != nil {
		c.Close()
		return
	}
	if rsp.StatusCode < 200 || rsp.StatusCode > 299 {
		c.Close()
		err = fmt.Errorf("http repsonse error status code %d", rsp.StatusCode)
	}
 	return
}

func NewDialer() *Dialer {
	return &Dialer{HttpDialer: transport.NewDefaultHttpDialer(transport.NewTaker)}
}
 
var dialer = NewDialer()

type server struct {
	transport.CloserServer
}

func (s *server) Listen(p xnet.Point, handle transport.Handle) (err error) {
	fn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { 
		f, ok := w.(http.Flusher)	
		if !ok || p.Path != "" && r.URL.Path != p.Path {
			log.Printf("deny illegal access path %s\n", r.URL.Path)
			w.WriteHeader(404)
			return
		} 
		w.WriteHeader(200)
		f.Flush()
		hj, ok := w.(http.Hijacker)
		if !ok {
			log.Println("hijack failed")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			log.Println(err)
			return
		}
		defer conn.Close()
		handle(conn)
	})

	svr := &http.Server{
		Addr:    p.Host,
		Handler: fn,
	}
	s.Closer = svr

	if p.Transport == "https" {
		err = svr.ListenAndServeTLS(p.CertFile, p.KeyFile)
	} else {
		err = svr.ListenAndServe()
	}
	return
}

func NewServer() transport.Server {
	return &server{}
}

func init() {
	transport.RegServerFn("http", NewServer)
	transport.RegDialFn("http", dialer.Dial)

	transport.RegServerFn("https", NewServer)
	transport.RegDialFn("https", dialer.Dial)
}
