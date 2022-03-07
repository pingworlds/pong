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

	STATUS_NORMAL  = 0
	STATUS_CLOSING = 1
	STATUS_CLOSED  = 2
)

// var Per_Max_Count int = 3
// var Conn_TimeOut int64 = 1 * 30
// var Stream_Idle_TimeOut int64 = 1 * 60
// var Stream_Close_Time time.Duration = 3
// var Peek_Time time.Duration = 30
// var Max_Live int64 = 1 * 60 * 60

var Per_Max_Count int = 50
var Conn_TimeOut int64 = 1 * 60
var Stream_Idle_TimeOut int64 = 5 * 60
var Stream_Close_Time time.Duration = 3
var Peek_Time time.Duration = 30
var Max_Live int64 = 1 * 60 * 60

var Frame_Buf_Size uint16 = 8192
var cfg = config.Config

func NewPong(p *proxy.Proto) pong {
	if cfg != nil {
		if cfg.PerMaxCount < 10 {
			Per_Max_Count = cfg.PerMaxCount
		} else if cfg.PerMaxCount > 300 {
			Per_Max_Count = 300
		}
		if cfg.WriteBuffer > 2048 {
			Frame_Buf_Size = uint16(cfg.WriteBuffer)
		}
	}

	pg := pong{Proto: p, connPool: &connPool{id: uuid.NewString(), conns: []*pongConn{}}}
	go pg.start()
	return pg
}

type connPool struct {
	id     string
	conns  []*pongConn
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
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
	var t *pongConn
	n := len(cp.conns)

	for i := 0; i < n; {
		t = cp.conns[i]
		if t.old && i < n-1 && !cp.conns[n-1].old {
			t = cp.conns[n-1]
			cp.conns[n-1] = cp.conns[i]
			cp.conns[i] = t
			n--
		}
		i++
		if t.old {
			continue
		}

		if len(t.streams) < Per_Max_Count {
			pc = t
			break
		}
	}
	return
}
 
func (cp *connPool) close() {
	cp.clear()
	if cp.cancel != nil {
		cp.cancel()
	}
	cp.conns = nil
}

func (cp *connPool) clear() {
	for _, c := range cp.conns {
		c.CloseWithCode(proxy.ERR_FORCE_CLOSE)
	}
}

func (cp *connPool) start() {
	tiker := time.NewTicker(Peek_Time * time.Second)
	defer tiker.Stop()
	cp.ctx, cp.cancel = context.WithCancel(context.Background())
	for {
		select {
		case <-cp.ctx.Done():
			return
		case <-tiker.C:
			t1 := time.Now().UnixMilli()
			log.Printf("pool[%s]  conns cap  %d  len:%d\n", cp.id, cap(cp.conns), len(cp.conns))
			for _, pc := range cp.conns {
				log.Printf("raw conn streams len:%d\n", len(pc.streams))
				if pc.closing {
					continue
				}

				if (t1-pc.created)/1000 > Max_Live {
					pc.old = true
				}
				if len(pc.streams) <= 0 {
					if pc.idling {
						dt := (t1 - pc.idleTime) / 1000
						if dt > Conn_TimeOut {
							pc.CloseWithCode(proxy.ERR_CONN_TIMEOUT)
							log.Printf("raw connection released (idle %d second)\n", dt)
						}
					} else {
						pc.idling = true
						pc.idleTime = t1
					}
				} else {
					pc.idling = false
					for _, s := range pc.streams {
						dt := min(t1-s.lastRead, t1-s.lastWrite) / 1000
						if dt > Stream_Idle_TimeOut {
							s.CloseWithError(proxy.ERR_CONN_TIMEOUT)
							log.Printf("stream[%d]released (idle %d second)(%s)\n", s.id, dt, s.t.Addr)
						}
					}
				}
			}
		}
	}
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

func (p *pong) ClearConn() {
	p.clear()
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
	p.connPool.close()
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
	id       string
	rbuf     []byte
	wbuf     []byte
	streams  map[uint32]*stream
	p        *pong
	Do       proxy.Do
	closing  bool
	errCode  byte
	isLocal  bool
	ctx      context.Context
	cancel   context.CancelFunc
	wmu      sync.Mutex
	smu      sync.Mutex
	n        uint32
	created  int64
	idleTime int64
	idling   bool
	old      bool
}

func (pc *pongConn) NextId() (n uint32) {
	pc.smu.Lock()
	defer pc.smu.Unlock()
	n = pc.n
	pc.n += 2
	return
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
	pc.smu.Lock()
	defer pc.smu.Unlock()
	delete(pc.streams, sid)
}

func (pc *pongConn) NewStream(id uint32, t *proxy.Tunnel) *stream {
	s := pc.new(id, t)
	t.Src = s
	t.SrcPeer = pc.p
	pc.p.OnTunnelOpen(t)
	return s
}
func (pc *pongConn) OpenStream(t *proxy.Tunnel) *stream {
	s := pc.new(pc.NextId(), t)
	t.Dst = s
	t.DstPeer = pc.p
	pc.p.OnTunnelOpen(t)
	return s
}

func (pc *pongConn) new(id uint32, t *proxy.Tunnel) *stream {
	ctx, cancel := context.WithCancel(pc.ctx)
	pr, pw := io.Pipe()
	tt := time.Now().UnixMilli()
	s := &stream{
		id:        id,
		t:         t,
		pc:        pc,
		pw:        pw,
		pr:        pr,
		frmCh:     make(chan *frame, 50),
		ctx:       ctx,
		cancel:    cancel,
		lastRead:  tt,
		lastWrite: tt,
	}
	pc.Put(s.id, s)
	go s.Listen()
	return s
}

func min(a, b int64) int64 {
	if a > b {
		return b
	}
	return a
}

func (pc *pongConn) Listen() {
	for {
		select {
		case <-pc.ctx.Done():
			return
		default:
			if err := pc.ReadFrame(); err != nil {
				pc.CloseWithError(err)
				return
			}
		}
	}
}

func (pc *pongConn) Close() error {
	pc.CloseWithCode(0)
	return nil
}

func (pc *pongConn) CloseWithError(err error) {
	pc.CloseWithCode(proxy.GetErrorCode(err, proxy.ERR_DST))
}

func (pc *pongConn) CloseWithCode(code byte) {
	defer func() {
		log.Println("raw conn release......")
	}()
	if pc.closing {
		return
	}
	pc.closing = true
	pc.old = true
	pc.errCode = code
	log.Printf("raw conn close (code=%d)\n", code)
	pc.Release()
	pc.cancel()
	pc.Conn.Close()
	pc.rbuf = nil
	pc.wbuf = nil
	pc.streams = nil
	pc.p.Remove(pc.id)
}

func (pc *pongConn) Release() {
	for _, s := range pc.streams {
		s.CloseWithError(pc.errCode)
	}
}

func (pc *pongConn) writeOne(sid uint32, ftype byte, b []byte) (n int, err error) {
	pc.wmu.Lock()
	defer pc.wmu.Unlock()
	if pc.closing {
		err = fmt.Errorf("on writeOne raw connection closed")
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
	if pc.closing {
		err = fmt.Errorf("raw connection closed on writing")
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
		return fmt.Errorf("frame too short")
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

		if n > 128 {
			return fmt.Errorf("dst address too long (%d > 128)", n)
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
				log.Printf("frame ignored, stream[%d] is released (%s)\n", sid, s.t.Addr)
			} else {
				log.Printf("frame ignored, stream[%d] not exsit\n", sid)
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

				payload := PayloadPool.Get().(*[]byte)
				if _, err = io.ReadFull(pc.Conn, (*payload)[0:m]); err != nil {
					return err
				}
				if !s.closed {
					f := Frames.New(ftype, sid, FRAME_DATA)
					f.payload = payload
					f.size = m
					s.frmCh <- f
					i += m
				}
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
			// f := Frames.NewCommandFrame(ftype, sid, code)
			if !s.closed {
				f := Frames.New(ftype, sid, code)
				s.frmCh <- f
			}
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
	if s.closed {
		return 0, fmt.Errorf("read error, stream is closed")
	}
	s.lastRead = time.Now().UnixMilli()
	return s.pr.Read(b)
}

func (s *stream) Write(b []byte) (n int, err error) {
	if s.closed {
		return 0, fmt.Errorf("write error, stream is closed")
	}
	s.lastWrite = time.Now().UnixMilli()
	return s.WriteRaw(FRAME_DATA, b)
}

func (s *stream) WriteRaw(ftype byte, payload []byte) (n int, err error) {
	return s.pc.WriteRaw(s.id, ftype, payload)
}

func (s *stream) Finish() error {
	_, err := s.WriteRaw(FRAME_FINISH, nil)
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
	// log.Printf("stream[%d] released (%s)", s.id, s.t.Addr)
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
	s.WriteRaw(FRAME_RST, payload)
	log.Printf("stream[%d] released with error %s (%s)\n", s.id, err.Error(), s.t.Addr)
	s.release()

}

func (s *stream) release() {
	defer func() {
		log.Printf("stream [%d]  released\n", s.id)
	}()
	s.cancel()
	s.t.Close() //close tunnel
	s.pr.Close()
	s.pw.Close()
	s.frmCh = nil
}

func (s *stream) CloseByRemote(code byte) (err error) {
	err = fmt.Errorf("error code %d %s", code, proxy.ErrTip[code])
	log.Printf("stream[%d] forcibly release by remote, %s  (%s)\n", s.id, err.Error(), s.t.Addr)
	if s.t != nil {
		s.t.AddError(err, "")
		log.Println(s.t.Errors)
	}
	s.Close()
	return
}

func (s *stream) handleFrame(f *frame) (err error) {
	switch f.ftype {
	case FRAME_DATA:
		buf := *f.payload
		if _, err = s.pw.Write(buf[:f.size]); err != nil {
			s.CloseWithError(proxy.ERR_NET)
		}
	case FRAME_FINISH: //remote send data over, half close
		s.pw.CloseWithError(io.EOF)
	case FRAME_CLOSE:
		s.Close()
	case FRAME_RST: //force close
		s.CloseByRemote(f.code)
	default:
		s.CloseWithError(proxy.ERR_COMMAND_UNKNOWN)
	}
	// Frames.PutCommandFrame(f)

	Frames.Put(f)
	return
}

var PayloadPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, Frame_Buf_Size)
		return &buf
	},
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
	ftype   byte
	sid     uint32
	payload *[]byte //payload buf
	size    uint16  //payload size
	code    byte
}

var Frames = frameCache{
	p: sync.Pool{
		New: func() interface{} {
			return &frame{}
		},
	},
}

type frameCache struct {
	p sync.Pool
}

func (fc *frameCache) New(ftype byte, sid uint32, code byte) *frame {
	f := fc.p.Get().(*frame)
	f.sid = sid
	f.code = code
	f.ftype = ftype
	f.size = 0
	return f
}

func (fc *frameCache) Put(f *frame) {
	if f.ftype == FRAME_DATA {
		PayloadPool.Put(f.payload)
	}
	f.payload = nil
	fc.p.Put(f)
}

type Local struct{ pong }

func (l Local) BeforeSend(t *proxy.Tunnel) (err error) {
	s := t.Dst.(*stream)
	_, err = s.WriteRaw(t.Method, t.Addr)
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
