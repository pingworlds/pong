package xnet

import (
	"io"
	"net"
	"strconv"
	"time"
)

const (
	IPV4 byte = 1

	DOMAIN byte = 3

	IPV6 byte = 4
)

/**
  +----+-----+-------+------+----+
  | ATYP | DST.ADDR | DST.PORT |
  +----+-----+-------+------+----+
  |  1   | Variable |     2    |
  +----+-----+-------+------+----+
*/
type Addr []byte

func (a Addr) Type() byte {
	return a[0]
}

func (a Addr) HostAndPort() (host string, port int) {
	n := 5
	switch a[0] {
	case IPV4:
		host = net.IP(a[1:5]).String()
	case IPV6:
		n = 17
		host = net.IP(a[1:17]).String()
	case DOMAIN:
		n = 2 + int(a[1])
		host = string(a[2:n])
	}
	port = int(a[n])<<8 | int(a[1+n])
	return
}

func (a Addr) String() string {
	host, port := a.HostAndPort()
	return host + ":" + strconv.Itoa(port)
}

func (a Addr) IP() net.IP {
	if a[0] == DOMAIN {
		return nil
	}
	host, _ := a.HostAndPort()
	return net.ParseIP(host)
}

func ReadAddr(r io.Reader) (Addr, error) {
	b := make([]byte, 2)
	_, err := ReadFullWithTimeout(r, b, 10000)
	if err != nil {
		return nil, err
	}
	n := 7 //ip v4
	switch b[0] {
	case IPV6:
		n = 19
	case DOMAIN:
		n = int(b[1]) + 4
	}

	b2 := make([]byte, n)
	copy(b2[0:2], b[0:2])
	io.ReadFull(r, b2[2:n])
	return Addr(b2), nil
}

func Dial(addr Addr) (conn net.Conn, err error) {
	return dial("tcp", addr)
}

func DialUDP(addr Addr) (conn net.Conn, err error) {
	return dial("udp", addr)
}

func dial(network string, addr Addr) (conn net.Conn, err error) {
	host, port := addr.HostAndPort()
	return net.DialTimeout(network, net.JoinHostPort(host, strconv.Itoa(port)), time.Duration(time.Second*5))
}

func DomainToAddr(host string, port int) Addr {
	addr := []byte(host)
	n := 4 + len(addr)
	b := make([]byte, n)
	b[0] = DOMAIN
	b[1] = byte(len(addr))
	copy(b[2:n-2], addr)
	b[n-2] = byte(port >> 8)
	b[n-1] = byte(port & 0xFF)
	return Addr(b)
}

type Doh struct {
	Disabled bool
	Name     string
	Path     string
	Method   string //get or post
}

var TimeoutError = &timeoutError{}

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "src/dst timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

var BreakError = &breakError{}

type breakError struct{}

func (e *breakError) Error() string { return "break" }

func NewByteReader() *ByteReader {
	return &ByteReader{}
}

type ByteReader struct {
	Break bool
}

func (br *ByteReader) Read(r io.Reader, b []byte) (n int, err error) {
	return br.read(r, b, false, 0)
}

func (br *ByteReader) ReadWithTimeout(r io.Reader, b []byte, timeout int64) (n int, err error) {
	return br.read(r, b, false, timeout)
}

func (br *ByteReader) ReadFull(r io.Reader, b []byte) (n int, err error) {
	return br.read(r, b, true, 0)
}

func (br *ByteReader) ReadFullWithTimeout(r io.Reader, b []byte, timeout int64) (n int, err error) {
	return br.read(r, b, true, timeout)
}

func (br *ByteReader) read(r io.Reader, b []byte, full bool, timeout int64) (n int, err error) {
	var nr int
	var t, t2 int64
	if timeout > 0 {
		t = time.Now().UnixMilli()
	}
	for {
		if br.Break {
			err = BreakError
			return
		}

		nr, err = r.Read(b[n:])
		if nr > 0 {
			n = n + nr
			if n == len(b) {
				return
			}
		}
		if err != nil || !full {
			return
		}

		if timeout > 0 {
			t2 = time.Now().UnixMilli()
			if t2-t > timeout {
				err = TimeoutError
				return
			}
			t = t2
		}
	}

}

var br = NewByteReader()

func Read(r io.Reader, b []byte) (n int, err error) {
	return br.Read(r, b)
}

func ReadWithTimeout(r io.Reader, b []byte, timeout int64) (n int, err error) {
	return br.ReadWithTimeout(r, b, timeout)
}

func ReadFull(r io.Reader, b []byte) (n int, err error) {
	return br.ReadFull(r, b)
}

func ReadFullWithTimeout(r io.Reader, b []byte, timeout int64) (n int, err error) {
	return br.ReadFullWithTimeout(r, b, timeout)
}
