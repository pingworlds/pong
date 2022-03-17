package ws

import (
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/pingworlds/pong/xnet"
	"github.com/pingworlds/pong/xnet/transport"
)

var upgrader = websocket.Upgrader{}

type Dialer struct {
	transport.HttpDialer
}

func (d Dialer) Dial(p *xnet.Point) (conn net.Conn, err error) {
	cfg := d.GetConfig(p)
	wd := websocket.DefaultDialer
	if p.Transport == "wss" {
		wd = &websocket.Dialer{TLSClientConfig: cfg.TLSConfig, HandshakeTimeout: 5 * time.Second}
	}
	log.Println(cfg)
	var wc *websocket.Conn
	if wc, _, err = wd.Dial(cfg.URL.String(), nil); err != nil {
		return
	}
	conn = NewConn(wc)
	return
}

type server struct {
	transport.CloserServer
}

func (s *server) Listen(p *xnet.Point, handle transport.Handle) (err error) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { 
		wc, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			return
		}
		src := NewConn(wc)
		defer src.Close()
		handle(src)
	})

	svr := &http.Server{Addr: p.Host, Handler: h}
	s.Closer = svr
	if p.Transport == "wss" {
		err = svr.ListenAndServeTLS(p.CertFile, p.KeyFile)
	} else {
		err = svr.ListenAndServe()
	}
	return
}

func NewConn(wc *websocket.Conn) *conn {
	pr, pw := io.Pipe()
	c := &conn{
		pr: pr,
		pw: pw,
		wc: wc,
	}
	return c
}

type conn struct {
	net.Conn
	wc *websocket.Conn
	pr *io.PipeReader
	pw *io.PipeWriter
	f  bool
}

func (c conn) receive() {
	for {
		p, err := c.readFrame()
		if err != nil {
			c.pw.CloseWithError(err)
			return
		}
		c.pw.Write(p)
	}
}

func (c conn) Write(b []byte) (n int, err error) {
	n = len(b)
	err = c.wc.WriteMessage(websocket.BinaryMessage, b)
	return
}

func (c *conn) Read(p []byte) (int, error) {
	if !c.f {
		c.f = true
		go c.receive()
	}

	return c.pr.Read(p)
}

func (c conn) readFrame() (p []byte, err error) {
	var t int
	if t, p, err = c.wc.ReadMessage(); err != nil {
		return
	}
	if t != websocket.BinaryMessage {
		err = xnet.Err_NotBinaryMessage
	}
	return
}

func (c conn) Close() error {
	c.pr.Close()
	c.pw.Close()
	return c.wc.Close()
}

func NewServer() transport.Server {
	return &server{}
}

func NewDialer() transport.Dialer {
	return &Dialer{HttpDialer: transport.NewDefaultHttpDialer(transport.NewTaker)}
}

var dialer = NewDialer()

func init() {
	transport.RegServerFn("ws", NewServer)
	transport.RegDialFn("ws", dialer.Dial)

	transport.RegServerFn("wss", NewServer)
	transport.RegDialFn("wss", dialer.Dial)
}
