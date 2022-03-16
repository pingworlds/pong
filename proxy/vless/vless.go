package vless

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
	CONNECT   byte = 1
	ASSOCIATE byte = 2
	IPV4      byte = 1
	DOMAIN    byte = 2
	IPV6      byte = 3
)

type vless struct {
	*proxy.Proto
}


func (v vless) Handle(src net.Conn) {
	defer src.Close()
	method, addr, err := v.readMethod(src)
	if err != nil {
		log.Printf("read cmd %x %v\n", method, err)
		return
	}
	v.Do(v.NewTunnel(uuid.NewString(), src, method, addr))
}

func (v vless) readMethod(src net.Conn) (method byte, addr xnet.Addr, err error) {
	b := *kit.Byte23.Get().(*[]byte)
	defer kit.Byte23.Put(&b)

	if _, err = v.ReadFull(src, b); err != nil {
		return
	}
	if v.isVaild(string(b[1:17])) {
		err = fmt.Errorf("unknown client id %s", string(b[1:17]))
		return
	}
	if b[17] != 0 {
		err = fmt.Errorf("not support")
		return
	}

	method = b[18]
	if method == ASSOCIATE {
		return
	}

	atype := b[21]
	n := 3 //ip v4
	switch atype {
	case IPV6:
		n = 15
		b[21] = xnet.IPV6
	case DOMAIN:
		n = int(b[22])
		b[21] = xnet.DOMAIN
	}

	b2 := make([]byte, n+4)
	if _, err = v.ReadFull(src, b2[2:n+2]); err != nil {
		return
	}

	copy(b2[0:2], b[21:23])
	copy(b2[n+2:n+4], b[19:21])
	addr = xnet.Addr(b2)
	return
}

func (v vless) isVaild(clientId string) (ok bool) {
	for _, v := range v.Clients {
		if v == clientId {
			ok = true
			return
		}
	}
	return
}

func (v vless) writeMethod(t *proxy.Tunnel) (err error) {
	var id uuid.UUID
	if id, err = uuid.Parse(v.Clients[0]); err != nil {
		return
	}
	a := t.Addr
	n := len(a)
	m := 19 + n
	b := make([]byte, m)
	copy(b[1:17], id[:16]) //client id
	b[18] = t.Method
	if b[18] == proxy.ASSOCIATE {
		b[18] = ASSOCIATE
	}

	copy(b[19:21], a[n-2:n]) //port
	copy(b[21:m], a[:n-2])   //addr

	if b[21] == xnet.DOMAIN {
		b[21] = DOMAIN
	} else if b[21] == xnet.IPV6 {
		b[21] = IPV6
	}
	_, err = t.Dst.Write(b)
	return
}

type Remote struct {
	vless
}

func (r Remote) AfterDial(t *proxy.Tunnel, err error) error {
	b := *kit.Byte7.Get().(*[]byte)
	defer kit.Byte7.Put(&b)
	var n = 2
	if err != nil {
		n = 4
		_ = append(b[:0],
			0,
			1,
			0,
			proxy.GetErrorCode(err))
	} else {
		_ = append(b[:0], 0)
	}
	t.Src.Write(b[:n])

	return err
}

type Local struct {
	vless
}

func (l Local) BeforeSend(t *proxy.Tunnel) error {
	return l.writeMethod(t)
}

//skip header
func (l Local) BeforeReceive(t *proxy.Tunnel) (err error) {
	b := *kit.Byte3.Get().(*[]byte)
	defer kit.Byte3.Put(&b)
	if _, err = l.ReadFull(t.Dst, b[:2]); err != nil {
		return err
	}
	if b[1] == 1 {
		l.ReadFull(t.Dst, b[:1])
		s := proxy.ErrTip[b[0]]
		if s == "" {
			s = proxy.ErrTip[proxy.ERR]
		}
		err = fmt.Errorf(s)
	} else if b[1] > 0 {
		io.CopyN(ioutil.Discard, t.Dst, int64(b[1]))
	}
	return
}

func NewLocal(p *proxy.Proto) proxy.Peer {
	return &Local{vless: vless{Proto: p}}
}

func NewRemote(p *proxy.Proto) proxy.Peer {
	return &Remote{vless: vless{Proto: p}}
}

func init() {
	proxy.RegProtocol("vless", &proxy.Protocol{
		Name:     "vless",
		LocalFn:  NewLocal,
		RemoteFn: NewRemote,
	})
}
