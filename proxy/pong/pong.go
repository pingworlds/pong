package pong

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/pingworlds/pong/config"
	"github.com/pingworlds/pong/kit"
	"github.com/pingworlds/pong/proxy"
	"github.com/pingworlds/pong/xnet"
	"github.com/pingworlds/pong/xnet/transport"
)

const (
	VER        byte   = 0
	HEADER_LEN uint16 = 7
	MTU        int    = 65535
	MUU        int    = 65500

	FRAME_DATA      byte = 0 //data
	FRAME_CONNECT   byte = 1 //connect
	FRAME_ASSOCIATE byte = 3 //udp associate
	FRAME_RELAY     byte = 5 //udp relay
	FRAME_FINISH    byte = 7 //finish write
	FRAME_CLOSE     byte = 8 //close stream
	FRAME_RST       byte = 9 //rst stream

	CONN_CREATE byte = 1
	CONN_CLOSED byte = 3
)

var Per_Max_Count int = 100
var Conn_TimeOut int64 = 1 * 60 * 1000
var Stream_Idle_Time time.Duration = 2 * time.Minute
var Stream_Close_Timeout time.Duration = 3 * time.Second
var Peek_Time = 2 * 60 * time.Second

// var Max_Live_Time = 1 * 60 * time.Minute

var Frame_Buf_Size uint16 = 3072

func NewPong(p *proxy.Proto) pong {
	if config.Config != nil && config.Config.PerMaxCount > 10 {
		Per_Max_Count = config.Config.PerMaxCount
	}
	return pong{Proto: p, connPool: &connPool{conns: []*pongConn{}}}
}

type connPool struct {
	conns []*pongConn
	mu    sync.Mutex
}

func (cp *connPool) Put(pc *pongConn) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.conns = append(cp.conns, pc)
}

func (cp *connPool) Remove(id string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	n := len(cp.conns)
	var i int
	var c *pongConn
	for i, c = range cp.conns {
		if c.id == id {
			break
		}
	}
	if i >= n {
		return
	}
	cp.conns[i] = cp.conns[n-1]
	cp.conns = cp.conns[:n-1]
}

func (cp *connPool) GetIdle() (pc *pongConn) {
	for _, c := range cp.conns {
		if c.closed {
			continue
		}
		if pc == nil || len(pc.streams) > len(c.streams) {
			pc = c
		}
		if len(pc.streams) <= 50 {
			break
		}
	}
	if pc != nil && len(pc.streams) > Per_Max_Count {
		pc = nil
	}
	return
}

func (cp *connPool) ReleaseAll() {
	for _, c := range cp.conns {
		c.release()
	}
	cp.conns = cp.conns[:0]
}

type pong struct {
	*proxy.Proto
	*connPool
}

func (p pong) Handle(src net.Conn) {
	defer src.Close()
	ver, err := p.Who(src)
	if err != nil {
		log.Printf("pong connection header error(%v)\n", err)
		return
	}
	pc := p.NewPongConn(src, ver, p.Do, false)
	pc.Listen()
}

/**
  connection header
  +----+-----+-------+
  | VER  | CLIENT_ID |
  +----+-----+-------+
  |  1   |    16     |
  +----+-----+-------+
*/
//just say who am i, server no response
func (p pong) Hi(dst net.Conn, ver byte, clientId uuid.UUID) (err error) {
	b := *kit.Byte17.Get().(*[]byte)
	defer kit.Byte17.Put(&b)
	b[0] = ver
	copy(b[1:17], clientId[:16])
	_, err = dst.Write(b)
	return
}

func (p pong) Who(src net.Conn) (ver byte, err error) {
	b := *kit.Byte17.Get().(*[]byte)
	defer kit.Byte17.Put(&b)
	if _, err = io.ReadFull(src, b); err != nil {
		return
	}
	ver = b[0]
	var id uuid.UUID
	if id, err = uuid.FromBytes(b[1:17]); err != nil {
		return
	}
	s := id.String()
	for _, v := range p.Clients {
		if v == s {
			return
		}
	}
	err = fmt.Errorf("deny unknown client")
	return
}

//override
func (p *pong) Open(t *proxy.Tunnel) (err error) {
	pc := p.GetIdle()
	if pc == nil {
		var clientId uuid.UUID
		if clientId, err = uuid.Parse(p.Clients[0]); err != nil {
			return
		}
		var rc net.Conn
		if rc, err = transport.Dial(p.Point); err != nil {
			return
		}
		if err = p.Hi(rc, VER, clientId); err != nil {
			return
		}

		pc = p.NewPongConn(rc, VER, proxy.LocalDo, true)
		go pc.Listen()
	}
	if pc != nil {
		pc.OpenStream(t)
	}
	return
}

func (p *pong) NewPongConn(conn net.Conn, ver byte, do proxy.Do, isLocal bool) *pongConn {
	ctx, cancel := context.WithCancel(context.Background())
	pc := &pongConn{
		Conn:    conn,
		id:      uuid.NewString(),
		n:       1,
		p:       p,
		Do:      do,
		streams: map[uint32]*stream{},
		rbuf:    make([]byte, 7),
		wbuf:    make([]byte, Frame_Buf_Size+7),
		ctx:     ctx,
		cancel:  cancel,
		isLocal: isLocal,
		created: time.Now().UnixMilli(),
	}
	p.Put(pc)
	return pc
}

func (p *pong) Close() error {
	p.connPool.ReleaseAll()
	return nil
}

func (p *pong) Stat() *proxy.PointStat {
	sn := 0
	for _, c := range p.conns {
		sn += len(c.streams)
	}
	return proxy.NewPointStat(p.Proto, len(p.conns), sn)
}

type pongConn struct {
	net.Conn
	id      string
	rbuf    []byte
	wbuf    []byte
	streams map[uint32]*stream
	p       *pong
	Do      proxy.Do
	closed  bool
	// old     bool
	created int64
	isLocal bool
	ctx     context.Context
	cancel  context.CancelFunc
	wmu     sync.Mutex
	smu     sync.Mutex
	n       uint32
}

func (pc *pongConn) Put(sid uint32, s *stream) {
	pc.smu.Lock()
	defer pc.smu.Unlock()
	pc.streams[sid] = s
}

func (pc *pongConn) Get(sid uint32) *stream {
	pc.smu.Lock()
	defer pc.smu.Unlock()
	return pc.streams[sid]
}

func (pc *pongConn) Remove(sid uint32) {
	go func() {
		time.AfterFunc(Stream_Close_Timeout, func() {
			pc.smu.Lock()
			defer pc.smu.Unlock()
			delete(pc.streams, sid)
		})
	}()
}

func (pc *pongConn) NewStream(id uint32, t *proxy.Tunnel) *stream {
	s := pc.new(id, t)
	t.Src = s
	t.SrcPeer = pc.p
	pc.p.OnTunnelOpen(t)
	return s
}
func (pc *pongConn) OpenStream(t *proxy.Tunnel) *stream {
	pc.n += 2
	s := pc.new(pc.n, t)
	t.Dst = s
	t.DstPeer = pc.p
	pc.p.OnTunnelOpen(t)
	return s
}

func (pc *pongConn) new(id uint32, t *proxy.Tunnel) *stream {
	ctx, cancel := context.WithCancel(pc.ctx)
	pr, pw := io.Pipe()
	s := &stream{
		id:     id,
		t:      t,
		pc:     pc,
		pw:     pw,
		pr:     pr,
		frmCh:  make(chan *frame, 50),
		ctx:    ctx,
		cancel: cancel,
	}
	pc.Put(s.id, s)
	go s.Listen()
	return s
}

func min(a, b time.Duration) time.Duration {
	if a > b {
		return b
	}
	return a
}

func (pc *pongConn) Listen() {
	var err error
	tiker := time.NewTicker(Peek_Time)
	defer func() {
		tiker.Stop()
		if err == io.EOF {
			err = nil
		}
		pc.CloseWithError(err)
	}()
	go func() {
		for {
			if pc.closed {
				return
			}
			if err = pc.ReadFrame(); err != nil {
				log.Println(err)
				return
			}
		}
	}()

	var t int64
	idling := false
	for {
		select {
		case <-pc.ctx.Done():
			return
		case <-tiker.C:
			if pc.closed {
				return
			}
			t2 := time.Now().UnixMilli()
			if len(pc.streams) <= 0 {
				if idling {
					idle := t2 - t
					if idle > Conn_TimeOut {
						log.Printf("raw connection released (idle %d second)\n", idle/1000)
						return
					}
				} else {
					idling = true
					t = t2
				}
			} else {
				idling = false
				for _, s := range pc.streams {
					if min(time.Duration(t2-s.lastRead), time.Duration(t2-s.lastWrite)) > Stream_Idle_Time {
						s.CloseWithError(proxy.ERR_CONN_TIMEOUT)
					}
				}
			}

		}
	}
}

func (pc *pongConn) Close() error {
	pc.CloseWithError(nil)
	return nil
}

func (pc *pongConn) CloseWithError(err error) {
	pc.release()
	pc.p.Remove(pc.id)
}

func (pc *pongConn) release() {
	if pc.closed {
		return
	}
	pc.closed = true
	pc.cancel()
	for _, s := range pc.streams {
		s.CloseWithError(proxy.ERR_FORCE_CLOSE)
	}
}

func (pc *pongConn) writeOne(sid uint32, ftype byte, b []byte) (n int, err error) {
	pc.wmu.Lock()
	defer pc.wmu.Unlock()

	if pc.closed {
		err = fmt.Errorf("raw connection closed")
		return
	}
	n = 2 + 4 + 1 + len(b)

	_ = append(pc.wbuf[:0],
		byte(n>>8),
		byte(n),
		byte(sid>>24),
		byte(sid>>16),
		byte(sid>>8),
		byte(sid),
		byte(ftype))
	if len(b) > 0 {
		_ = copy(pc.wbuf[7:], b)
	}

	return pc.Conn.Write(pc.wbuf[:n])
}

func (pc *pongConn) WriteRaw(sid uint32, ftype byte, payload []byte) (n int, err error) {
	if pc.closed {
		err = fmt.Errorf("raw connection closed on writing")
		log.Println(err)
		return
	}
	n = len(payload)
	if n == 0 {
		return pc.writeOne(sid, ftype, nil)
	}

	var m int
	var max = int(Frame_Buf_Size)
	for i := 0; i < n; {
		m = max
		if i+m > n {
			m = n - i
		}
		if _, err = pc.writeOne(sid, ftype, payload[i:i+m]); err != nil {
			pc.CloseWithError(err)
			return
		}
		i += m
	}
	return
}

func (pc *pongConn) ReadFrame() error {
	var err error
	if _, err = io.ReadFull(pc.Conn, pc.rbuf[:7]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint16(pc.rbuf[0:2])
	if n < HEADER_LEN {
		return fmt.Errorf("bad frame too short")
	}
	sid := binary.BigEndian.Uint32(pc.rbuf[2:6])
	ftype := pc.rbuf[6]
	s := pc.Get(sid)
	n = n - 7
	if ftype == FRAME_CONNECT || ftype == FRAME_ASSOCIATE || ftype == FRAME_RELAY {
		if s != nil {
			s.CloseWithError(proxy.ERR_REPEAT_ID)
			return nil
		}

		b := *kit.Byte128.Get().(*[]byte)
		defer kit.Byte128.Put(&b)
		if _, err = io.ReadFull(pc.Conn, b[0:n]); err != nil {
			return err
		}
		var addr xnet.Addr
		if addr, err = xnet.ReadAddr(bytes.NewReader(b[:n])); err != nil {
			log.Println(err)
			s.CloseWithError(proxy.ERR_ADDR_TYPE)
			return nil
		}
		t := pc.p.NewTunnel(uuid.NewString(), nil, ftype, addr)
		t.Multiple = true
		s = pc.NewStream(sid, t)
		t.Src = s
		t.SrcPeer = pc.p
		go pc.Do(t)
	} else {
		if s == nil || s.closed {
			if s != nil {
				s.CloseWithError(proxy.ERR_CONN_TIMEOUT)
				log.Printf("stream[%d] %s is closed, frame ignored", sid, s.t.Addr)
			} else {
				log.Printf("stream[%d] not exsit, frame ignored", sid)
			}

			if n > 0 {
				if _, err = io.CopyN(ioutil.Discard, pc.Conn, int64(n)); err != nil {
					return err
				}
			}
			return nil
		}

		if ftype == FRAME_DATA {
			var m uint16
			var i uint16
			for i = 0; i < n; {
				m = Frame_Buf_Size
				if i+m > n {
					m = n - i
				}
				f := Frames.NewDataFrame(sid, m)

				if _, err = io.ReadFull(pc.Conn, f.buf[0:m]); err != nil {
					return err
				}
				s.frmCh <- f
				i += m
			}
		} else {
			var code byte
			if n > 0 {
				if _, err = io.ReadFull(pc.Conn, pc.rbuf[:1]); err != nil {
					return err
				}
				code = pc.rbuf[0]
				if n > 1 {
					if _, err = io.CopyN(ioutil.Discard, pc.Conn, int64(n-1)); err != nil {
						return err
					}
				}
			}
			f := Frames.NewCommandFrame(ftype, sid, code)
			s.frmCh <- f
		}
	}

	return nil
}

type stream struct {
	net.Conn
	id        uint32
	t         *proxy.Tunnel
	pc        *pongConn
	frmCh     chan *frame
	pr        *io.PipeReader
	pw        *io.PipeWriter
	ctx       context.Context
	cancel    context.CancelFunc
	lastRead  int64
	lastWrite int64
	closed    bool
}

func (s *stream) Read(b []byte) (n int, err error) {
	s.lastRead = time.Now().UnixMilli()
	if s.closed {
		s.pr.CloseWithError(io.EOF)
		return
	}
	return s.pr.Read(b)
}

func (s *stream) Write(b []byte) (n int, err error) {
	s.lastWrite = time.Now().UnixMilli()
	return s.pc.WriteRaw(s.id, FRAME_DATA, b)
}

func (s *stream) Finish() error {
	_, err := s.pc.WriteRaw(s.id, FRAME_FINISH, nil)
	return err
}

func (s *stream) Listen() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case f := <-s.frmCh:
			if f == nil {
				return
			}
			s.handleFrame(f)
		}
	}
}

func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.release()
	s.pc.Remove(s.id)
	return nil
}

func (s *stream) CloseWithError(code byte) {
	s.UnlockClose(code)
	s.pc.Remove(s.id)
}

func (s *stream) UnlockClose(code byte) {
	if s.closed {
		return
	}
	s.closed = true
	var payload []byte
	if code != 0 {
		payload = []byte{code}
	}
	err := fmt.Errorf(proxy.ErrTip[code])
	if s.t != nil {
		s.t.AddError(err, "")
	}
	log.Printf("stream[%d] %s closed with error, code:%d  %s\n", s.id, s.t.Addr, code, err.Error())
	s.pc.WriteRaw(s.id, FRAME_RST, payload)
	s.release()
}

func (s *stream) release() {
	s.cancel()
	s.t.Close() //close tunnel
	s.pr.Close()
	s.pw.Close()
}

func (s *stream) CloseByRemote(code byte) (err error) {
	err = fmt.Errorf("error code %d %s", code, proxy.ErrTip[code])
	log.Printf("stream[%d] %s forcibly closed by remote, %s", s.id, s.t.Addr, err.Error())
	if s.t != nil {
		s.t.AddError(err, "")
		log.Println(s.t.Errors)
	}
	s.Close()
	return
}

func (s *stream) handleFrame(f *frame) (err error) {
	if f.ftype == FRAME_DATA {
		if _, err = s.pw.Write(f.buf[:f.size]); err != nil {
			s.CloseWithError(proxy.ERR_NET)
		}
		Frames.PutDataFrame(f)
	} else {
		switch f.ftype {
		case FRAME_DATA:

		case FRAME_FINISH: //remote send data over, half close
			s.pw.CloseWithError(io.EOF)
		case FRAME_CLOSE:
			s.Close()
		case FRAME_RST: //force close
			s.CloseByRemote(f.code)
		default:
			s.CloseWithError(proxy.ERR_COMMAND_UNKNOWN)
		}
		Frames.PutCommandFrame(f)
	}
	return
}

/**
  frame header
  +----+-----+-------+------+-----+----+
  | LEN | STREAM_ID |  FRAME_TYPE  |
  +----+-----+-------+------+-----+----+
  |  2  |      4       |     1        |
  +----+-----+-------+------+-----+----+
*/
type frame struct {
	ftype byte
	sid   uint32
	buf   []byte //payload buf
	size  uint16 //payload size
	code  byte
}

var Frames = frameCache{
	p: sync.Pool{
		New: func() interface{} {
			return &frame{}
		},
	},
	dp: sync.Pool{
		New: func() interface{} {
			return &frame{buf: make([]byte, Frame_Buf_Size)}
		},
	},
}

type frameCache struct {
	p  sync.Pool
	dp sync.Pool
}

func (fc *frameCache) NewDataFrame(sid uint32, size uint16) *frame {
	f := fc.dp.Get().(*frame)
	f.size = size
	f.sid = sid
	return f
}

func (fc *frameCache) NewCommandFrame(ftype byte, sid uint32, code byte) *frame {
	f := fc.p.Get().(*frame)
	f.ftype = ftype
	f.code = code
	f.sid = sid
	return f
}

func (fc *frameCache) PutCommandFrame(f *frame) {
	fc.p.Put(f)
}
func (fc *frameCache) PutDataFrame(f *frame) {
	fc.dp.Put(f)
}

type Local struct{ pong }

func (l Local) BeforeSend(t *proxy.Tunnel) (err error) {
	s := t.Dst.(*stream)
	_, err = s.pc.WriteRaw(s.id, t.Method, t.Addr)
	return
}

func (l Local) AfterSend(t *proxy.Tunnel, err error) error {
	s := t.Dst.(*stream)
	if err == nil || err == io.EOF {
		err = s.Finish()
	} else {
		s.CloseWithError(proxy.GetErrorCode(err, proxy.ERR_NET))
	}
	return err
}

type Remote struct{ pong }

func (r Remote) AfterDial(t *proxy.Tunnel, err error) error {
	s := t.Src.(*stream)
	if err != nil {
		log.Println(err)
		s.CloseWithError(proxy.GetErrorCode(err, proxy.ERR_DST))
	}
	return err
}

func (r Remote) AfterReceive(t *proxy.Tunnel, err error) error {
	s := t.Src.(*stream)
	if err == nil || err == io.EOF {
		err = s.Finish()
	} else {
		log.Println(err)
		s.CloseWithError(proxy.GetErrorCode(err, proxy.ERR_DST))
	}
	return err
}

func NewLocal(p *proxy.Proto) proxy.Peer {
	return &Local{pong: NewPong(p)}
}

func NewRemote(p *proxy.Proto) proxy.Peer {
	return &Remote{pong: NewPong(p)}
}

func init() {
	proxy.RegProtocol("pong", &proxy.Protocol{
		Name:     "pong",
		LocalFn:  NewLocal,
		RemoteFn: NewRemote,
	})
}
