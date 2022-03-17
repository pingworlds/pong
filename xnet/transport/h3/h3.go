package h3

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	// "time"

	"github.com/lucas-clemente/quic-go"
	"github.com/lucas-clemente/quic-go/http3"
	"github.com/lucas-clemente/quic-go/quicvarint"

	"github.com/pingworlds/pong/xnet"
	"github.com/pingworlds/pong/xnet/transport"
)

var quicConfig = &quic.Config{
	KeepAlive:      true,  
}

type Taker struct {
	*transport.HttpTaker
}

func (t *Taker) GetSchema() string {
	return "https"
}

func (t *Taker) NewClient(cfg *tls.Config) (c *http.Client) {
	return &http.Client{Transport: &http3.RoundTripper{
		TLSClientConfig: cfg,
		QuicConfig:      quicConfig,
	}}
}

func (t *Taker) NewRequest(u *url.URL) *http.Request {
	return &http.Request{
		Method:     http.MethodPost,
		URL:        u,
		Proto:      "HTTP/3",
		ProtoMajor: 3,
		ProtoMinor: 0,
	}
}

func (t *Taker) Do(req *http.Request, cfg *transport.Config) (rsp *http.Response, err error) {
	client := t.NewClient(cfg.TLSConfig)
	return client.Do(req)
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

		w.WriteHeader(200)
		stream := w.(http3.DataStreamer).DataStream()
		conn := NewStream(stream)
		defer conn.Close()
		handle(conn)
	})

	svr := &http3.Server{
		Server: &http.Server{
			Addr:    p.Host,
			Handler: h,
		},
		QuicConfig: quicConfig,
	}

	s.Closer = svr
	err = svr.ListenAndServeTLS(p.CertFile, p.KeyFile)
	return
}

func NewStream(qs quic.Stream) *stream {
	pr, pw := io.Pipe()
	return &stream{
		pr: pr,
		pw: pw,
		s:  qs,
	}
}

type stream struct {
	net.Conn
	s  quic.Stream
	pr *io.PipeReader
	pw *io.PipeWriter
	f  bool
}

func (s stream) Write(b []byte) (n int, err error) {
	n = len(b)
	if n == 0 {
		return
	}
	buf := &bytes.Buffer{}
	quicvarint.Write(buf, 0x0)
	quicvarint.Write(buf, uint64(n))

	//write header
	if _, err = s.s.Write(buf.Bytes()); err != nil {
		return
	}
	//write payload
	return s.s.Write(b)
}

func (s *stream) Read(p []byte) (int, error) {
	if !s.f {
		s.f = true
		go s.receive()
	}
	return s.pr.Read(p)
}

func (s stream) Close() error {
	s.pr.Close()
	s.pw.Close()
	return s.s.Close()
}

func (s stream) readFrame() (p []byte, err error) {
	r := quicvarint.NewReader(s.s)
	var t, m uint64
	if t, err = quicvarint.Read(r); err != nil {
		return
	}
	if t != 0x0 {
		err = fmt.Errorf("incorrect HTTP3 frame type! %x", t)
		return
	}
	if m, err = quicvarint.Read(r); err != nil {
		return
	}
	p = make([]byte, m)
	_, err = io.ReadFull(s.s, p)
	return
}

func (s stream) receive() {
	for {
		p, err := s.readFrame()
		if err != nil {
			s.pw.CloseWithError(err)
			return
		}
		s.pw.Write(p)
	}
}

func NewServer() transport.Server {
	return &server{}
}

var dialer = NewDialer()

func init() {
	transport.RegServerFn("h3", NewServer)
	transport.RegDialFn("h3", dialer.Dial)
}
