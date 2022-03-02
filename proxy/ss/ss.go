package ss

import (
	"log"
	"net"

	"github.com/google/uuid"
	"github.com/pingworlds/pong/proxy"
	"github.com/pingworlds/pong/xnet"
)

type ss struct {
	*proxy.Proto
}

func (s ss) Handle(src net.Conn) {
	defer src.Close()
	addr, err := xnet.ReadAddr(src)
	if err != nil {
		log.Println(err)
		return
	}
	s.Do(s.NewTunnel(uuid.NewString(), src, proxy.CONNECT, addr))
}

/**
  socks5 header
  +----+-----+-------+------+----+
  | ATYP | DST.ADDR | DST.PORT |
  +----+-----+-------+------+----+
  |  1   | Variable |     2    |
  +----+-----+-------+------+----+
*/
func (s ss) WriteMethod(tun *proxy.Tunnel) (err error) {
	_, err = tun.Dst.Write(tun.Addr)
	return
}

type Local struct {
	ss
}

func (l Local) BeforeSend(tun *proxy.Tunnel) error {
	return l.WriteMethod(tun)
}

func NewLocal(p *proxy.Proto) proxy.Peer {
	return &Local{ss: ss{Proto: p}}
}

func NewRemote(p *proxy.Proto) proxy.Peer {
	return &ss{Proto: p}
}

func init() {
	proxy.RegProtocol("ss", &proxy.Protocol{
		Name:     "ss",
		LocalFn:  NewLocal,
		RemoteFn: NewRemote,
	})
}
