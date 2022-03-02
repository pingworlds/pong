package udp

import (
	"io"
	"log"
	"net"

	"github.com/pingworlds/pong/xnet"
	"github.com/pingworlds/pong/xnet/transport"
)

var UDP_MUU int = 1580

type Server interface {
	io.Closer
	Listen(p *xnet.Point, handle transport.HandleUDP) (err error)
	LocalAddr() *net.UDPAddr
}

func NewServer() *server {
	return &server{}
}

type server struct {
	transport.CloserServer
	addr *net.UDPAddr
}

func (s *server) LocalAddr() *net.UDPAddr {
	return s.addr
}

func (s *server) Listen(p *xnet.Point, handle transport.HandleUDP) (err error) {
	var addr *net.UDPAddr
	if addr, err = net.ResolveUDPAddr("udp", p.Host); err != nil {
		return
	}
	var conn *net.UDPConn
	if conn, err = net.ListenUDP("udp", addr); err != nil {
		return
	}

	s.addr, err = net.ResolveUDPAddr("udp", conn.LocalAddr().String())

	defer conn.Close()
	s.Closer = conn
	for {

		b := make([]byte, UDP_MUU)
		var n int
		var addr *net.UDPAddr
		if n, addr, err = conn.ReadFromUDP(b); err != nil {
			log.Println(err)
			continue
		}
		// xnet.ReadAddr(bytes.NewReader(frm.payload)))
		go handle(conn, b[:n], addr)
	}
}
