package tcp

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/pingworlds/pong/xnet"
	"github.com/pingworlds/pong/xnet/transport"
)

var Dialer = &net.Dialer{Timeout: time.Second * 5}

func DialTCP(p xnet.Point) (conn net.Conn, err error) {
	addr := p.Host
	var port string
	if _, _, err = net.SplitHostPort(p.Host); err != nil {
		port = "443"
		if p.Transport == "http" {
			port = "80"
		}
		addr = net.JoinHostPort(p.Host, port)
	}
	return Dialer.Dial("tcp", addr)
}

func DialUDP(p xnet.Point) (conn net.Conn, err error) {
	return Dialer.Dial("udp", p.Host)
}

type TLSDialer struct {
	transport.HttpDialer
}

func (d TLSDialer) Dial(p xnet.Point) (conn net.Conn, err error) {
	cfg := d.GetConfig(p)
	return tls.DialWithDialer(Dialer, "tcp", p.Host, cfg.TLSConfig)
}

func NewTLSDialer() *TLSDialer {
	return &TLSDialer{HttpDialer: transport.NewDefaultHttpDialer(transport.NewTaker)}
}

var tlsDialer = NewTLSDialer()

type server struct {
	transport.CloserServer
}

func (s *server) ReadyTLSConfig(p xnet.Point) (cfg *tls.Config, err error) {
	var cert tls.Certificate
	if cert, err = tls.LoadX509KeyPair(p.CertFile, p.KeyFile); err != nil {
		return
	}
	cfg = &tls.Config{Certificates: []tls.Certificate{cert}}
	return
}

func (s *server) Listen(p xnet.Point, handle transport.Handle) (err error) {
	var l net.Listener
	if p.Transport == "tls" {
		var cfg *tls.Config
		if cfg, err = s.ReadyTLSConfig(p); err != nil {
			return
		}
		if l, err = tls.Listen("tcp", p.Host, cfg); err != nil {
			return
		}
	} else if l, err = net.Listen("tcp", p.Host); err != nil {
		return
	}
	s.Closer = l
	for {
		var conn net.Conn
		if conn, err = l.Accept(); err != nil {
			return
		}
		go handle(conn)
	}
}

func NewServer() transport.Server {
	return &server{}
}

func init() {
	transport.RegServerFn("tcp", NewServer)
	transport.RegDialFn("tcp", DialTCP)

	transport.RegServerFn("tls", NewServer)
	transport.RegDialFn("tls", tlsDialer.Dial)
}
