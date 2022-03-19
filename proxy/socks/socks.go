package socks

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"

	"github.com/google/uuid"
	"github.com/pingworlds/pong/kit"
	"github.com/pingworlds/pong/proxy"
	"github.com/pingworlds/pong/xnet"
)

const (
	Q_SOCKS   byte = 7
	SOCKS_5   byte = 5
	CONNECT   byte = 1
	ASSOCIATE byte = 3
)

var rsp_ok = []byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}

type Socks struct {
	*proxy.Proto
	Ver byte
}

func (s Socks) Handle(src net.Conn) {
	defer src.Close()
	method, addr, err := s.handshake(src)
	if err != nil {
		log.Printf("handshake error mehod:%x  error:%v\n", method, err)
		return
	}
	s.Do(s.NewTunnel(uuid.NewString(), src, method, addr))
}

func (s Socks) handshake(src net.Conn) (method byte, addr xnet.Addr, err error) {
	b := *kit.Byte3.Get().(*[]byte)
	defer kit.Byte3.Put(&b)

	if _, err = io.ReadFull(src, b[:2]); err != nil {
		return
	}
	v := b[0]
	if v == Q_SOCKS {
		method = b[1]
	} else if v == SOCKS_5 {
		//skip methods auth part
		if _, err = io.CopyN(ioutil.Discard, src, int64(b[1])); err != nil {
			return
		}
		//write ok
		if _, err = src.Write([]byte{b[0], 0}); err != nil {
			return
		}
		if _, err = io.ReadFull(src, b[:3]); err != nil {
			return
		}
		method = b[1]
	} else {
		err = fmt.Errorf("not supprt socks verion")
		return
	}

	if addr, err = xnet.ReadAddr(src); err != nil {
		return
	}
	if v == SOCKS_5 {
		_, err = src.Write(rsp_ok)
	}
	return
}

/**
  socks5 header
  +----+-----+-------+------+------+----+
  |  CMD | ATYP | DST.ADDR  |  DST.PORT |
  +----+-----+-------+------+------+----+
  |   1  |  1   | Variable  |    2      |
  +----+-----+-------+------+------+----+
*/
func (s Socks) WriteMethod(t *proxy.Tunnel) (err error) {
	var h []byte
	if s.Ver == SOCKS_5 {
		h = []byte{SOCKS_5, proxy.CONNECT, 0x00}
	} else if s.Ver == Q_SOCKS {
		h = []byte{Q_SOCKS, proxy.CONNECT}
	}
	n := len(h) + len(t.Addr)
	b := make([]byte, n)
	copy(b[:len(h)], h)
	copy(b[len(h):n], t.Addr)
	_, err = t.Dst.Write(b)
	return
}

type Local struct {
	Socks
}

func (l Local) BeforeSend(t *proxy.Tunnel) (err error) { 
	b := *kit.Byte3.Get().(*[]byte)
	defer kit.Byte3.Put(&b)
	if l.Ver == SOCKS_5 {
		if _, err = t.Dst.Write([]byte{l.Ver, 0x00}); err != nil {
			return
		}
		if _, err = io.ReadFull(t.Dst, b[:2]); err != nil {
			return
		}
		if b[1] != 0 {
			err = fmt.Errorf("authentication not supported")
			return
		}
	}
	if err = l.WriteMethod(t); err != nil {
		return
	}

	if l.Ver == SOCKS_5 {
		_, err = io.CopyN(ioutil.Discard, t.Dst, int64(10))
	}
	return
}

func NewLocal(p *proxy.Proto) proxy.Peer {
	return &Local{Socks: Socks{Proto: p, Ver: SOCKS_5}}
}

func NewRemote(p *proxy.Proto) proxy.Peer {
	return &Socks{Proto: p, Ver: SOCKS_5}
}

func NewQSocksLocal(p *proxy.Proto) proxy.Peer {
	return &Local{Socks: Socks{Proto: p, Ver: Q_SOCKS}}
}

func init() {
	proxy.RegProtocol("socks", &proxy.Protocol{
		Name:     "socks",
		LocalFn:  NewLocal,
		RemoteFn: NewRemote,
	})

	proxy.RegProtocol("qsocks", &proxy.Protocol{
		Name:     "qsocks",
		LocalFn:  NewQSocksLocal,
		RemoteFn: NewRemote,
	})
}
