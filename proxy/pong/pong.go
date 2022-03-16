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
	VER             byte   = 0
	HEADER_LEN      uint16 = 7
	FRAME_DATA      byte   = 0 //data
	FRAME_CONNECT   byte   = 1 //connect
	FRAME_ASSOCIATE byte   = 3 //udp associate
	FRAME_RELAY     byte   = 5 //udp relay
	FRAME_FINISH    byte   = 7 //finish write
	FRAME_CLOSE     byte   = 8 //close stream
	FRAME_RST       byte   = 9 //rst stream
)

var Per_Max_Count int = 30
var Conn_TimeOut int64 = 1 * 60 * 1000
var Data_TimeOut int64 = 10 * 1000
var Peek_Time time.Duration = 30
var Max_Live int64 = 1 * 60 * 60 * 1000
var Frame_Buf_Size uint16 = 8192
var cfg = config.Config

func NewPong(p *proxy.Proto) pong {
	if cfg != nil {
		if cfg.PerMaxCount > 0 {
			if cfg.PerMaxCount > 300 {
				Per_Max_Count = 300
			} else {
				Per_Max_Count = cfg.PerMaxCount
			}
		}
		if cfg.WriteBuffer > 2048 {
			Frame_Buf_Size = uint16(cfg.WriteBuffer)
		}
	}

	connPool := &connPool{
		id:      uuid.New().ID(),
		conns:   []*pconn{},
		closeCh: make(chan closeReq),
	}

	pg := pong{Proto: p,
		br:       xnet.NewByteReader(),
		connPool: connPool,
	}
	pg.Filter = proxy.EmptyFilter
	go pg.start()
	return pg
}

type closeReq struct {
	stype byte
	o     interface{}
}

type connPool struct {
	id       uint32
	conns    []*pconn
	closeCh  chan closeReq
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	capacity int
}

func (cp *connPool) Put(pc *pconn) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.conns = append(cp.conns, pc)
	cp.capacity++
}

func (cp *connPool) removeConn(pc *pconn) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	n := len(cp.conns)
	var i int
	var c *pconn
	for i, c = range cp.conns {
		if c.id == pc.id {
			break
		}
	}
	if i >= n {
		return
	}
	cp.conns[i] = cp.conns[n-1]
	cp.conns[n-1] = nil
	cp.conns = cp.conns[:n-1]
	log.Printf("raw conn [%s] removed\n", pc.id[:4])
}

func (cp *connPool) GetIdle() (pc *pconn) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	var t *pconn
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
	cp.mu.Lock()
	defer cp.mu.Unlock()
	for _, c := range cp.conns {
		c.markClose(proxy.ERR_FORCE_CLOSE, false)
		c.clearStreams()
		c.Release()
	}
	cp.conns = []*pconn{}
}

func (cp *connPool) closeConn(pc *pconn) {
	pc.clearStreams()
	pc.Release()
	cp.removeConn(pc)
}

func (cp *connPool) start() {
	cp.ctx, cp.cancel = context.WithCancel(context.Background())
	go func() {
		tiker := time.NewTicker(Peek_Time * time.Second)
		defer tiker.Stop()
		for {
			select {
			case <-cp.ctx.Done():
				return

			case <-tiker.C:
				cp.checkTimeout()
			}
		}
	}()

	for {
		select {
		case <-cp.ctx.Done():
			return
		case req := <-cp.closeCh:
			if req.stype == 0 {
				return
			}
			if req.stype == 1 {
				cp.closeConn(req.o.(*pconn))
			} else if req.stype == 2 {
				s := req.o.(*stream)
				s.pc.ReleaseStream(s)
				// cp.closeStream(req.o.(*stream))
			}
		}
	}
}

// func (cp *connPool) closeStream(s *stream) {
// 	s.Release()
// 	s.pc.removeStream(s.id)
// }

func (cp *connPool) checkTimeout() {
	t1 := time.Now().UnixMilli()
	for _, pc := range cp.conns {
		if pc == nil || pc.closing {
			continue
		}
		// log.Printf("raw conn[%s] steams count %d\n", pc.id[:4], len(pc.streams))

		if !pc.old && (t1-pc.created) > Max_Live {
			log.Printf("mark raw conn [%s] old\n", pc.id[:4])
			pc.old = true
		}

		if len(pc.streams) > 0 {
			pc.idling = false

		} else {
			if pc.idling {
				dt := (t1 - pc.idleTime)
				if dt > Conn_TimeOut {
					pc.Close(proxy.ERR_FORCE_CLOSE)
					log.Printf("raw conn [%s] released (idle %d second) \n", pc.id[:4], dt/1000)
				}
			} else {
				pc.idling = true
				pc.idleTime = t1
			}
		}
	}
}

type pong struct {
	*proxy.Proto
	*connPool
	br *xnet.ByteReader
}

func (p pong) Handle(src net.Conn) {
	defer src.Close()
	ver, err := p.Who(src)
	if err != nil {
		log.Printf("read pong connection header error(%v) \n", err)
		return
	}
	pc := p.Newpconn(src, ver, p.Do, false)
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
	if _, err = p.ReadFull(src, b); err != nil {
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
	err = fmt.Errorf("unknown client")
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

		pc = p.Newpconn(rc, VER, proxy.LocalDo, true)
		go pc.Listen()
	}
	if pc != nil {
		pc.OpenStream(t)
	}
	return
}

func (p *pong) Newpconn(conn net.Conn, ver byte, do proxy.Do, isLocal bool) *pconn {
	ctx, cancel := context.WithCancel(context.Background())
	pc := &pconn{
		Conn:    conn,
		br:      xnet.NewByteReader(),
		id:      uuid.NewString(),
		n:       1,
		p:       p,
		Do:      do,
		streams: map[uint32]*stream{},
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

type pconn struct {
	net.Conn
	id       string
	br       *xnet.ByteReader
	rbuf     *[]byte
	wbuf     *[]byte
	streams  map[uint32]*stream
	p        *pong
	Do       proxy.Do
	isLocal  bool
	ctx      context.Context
	cancel   context.CancelFunc
	wmu      sync.Mutex
	smu      sync.Mutex
	n        uint32
	created  int64
	idleTime int64
	idling   bool
	closing  bool
	old      bool
}

func (pc *pconn) Get(sid uint32) *stream {
	pc.smu.Lock()
	defer pc.smu.Unlock()
	return pc.streams[sid]
}

func (pc *pconn) ReleaseStream(s *stream) {
	s.Release()
	pc.removeStream(s.id)
}

func (pc *pconn) removeStream(sid uint32) {
	pc.smu.Lock()
	defer pc.smu.Unlock()
	delete(pc.streams, sid)
}

func (pc *pconn) NewStream(id uint32, t *proxy.Tunnel) *stream {
	s := pc.new(id, t)
	t.Src = s
	t.SrcPeer = pc.p
	pc.p.OnTunnelOpen(t)
	return s
}
func (pc *pconn) OpenStream(t *proxy.Tunnel) *stream {
	s := pc.new(0, t)
	t.Dst = s
	t.DstPeer = pc.p
	pc.p.OnTunnelOpen(t)
	return s
}

func (pc *pconn) clearStreams() {
	pc.smu.Lock()
	defer pc.smu.Unlock()

	for _, s := range pc.streams {
		s.markClose(nil, 0, false)
		s.WriteCode(FRAME_CLOSE, 0)
		s.Release()
		delete(pc.streams, s.id)
	}
}

func (pc *pconn) new(id uint32, t *proxy.Tunnel) *stream {
	pc.smu.Lock()
	defer pc.smu.Unlock()

	if id == 0 {
		id = pc.n
		pc.n += 2
	}
	pr, pw := io.Pipe()
	s := &stream{
		id: id,
		pc: pc,
		stremmCtrl: &stremmCtrl{
			pr:    pr,
			pw:    pw,
			frmCh: make(chan *frame, 50),
			t:     t,
		},
	}

	pc.streams[id] = s
	go s.Listen()
	return s
}

var FrameWriteBuffer = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, Frame_Buf_Size+7)
		return &buf
	},
}

func (pc *pconn) MarkReleaseStream(s *stream) {
	pc.p.closeCh <- closeReq{stype: 2, o: s}
}

func (pc *pconn) markClose(code byte, releaseFlag bool) {
	pc.smu.Lock()
	defer pc.smu.Unlock()

	if pc.closing {
		return
	}
	pc.old = true
	pc.closing = true

	if releaseFlag {
		pc.p.closeCh <- closeReq{stype: 1, o: pc}
	}
}

func (pc *pconn) Close(code byte) {
	pc.markClose(code, true)
}

func (pc *pconn) Release() {
	pc.br.Break = true //stop I/O
	pc.cancel()        //cancel listening
	pc.Conn.Close()
	pc.rbuf = nil
	pc.wbuf = nil
	pc.streams = nil
	log.Printf("raw conn [%s] released  \n", pc.id[:4])
}

func (pc *pconn) writeOne(sid uint32, ftype byte, b []byte) (n int, err error) {
	pc.wmu.Lock()
	defer pc.wmu.Unlock()

	buf := *FrameWriteBuffer.Get().(*[]byte)
	defer FrameWriteBuffer.Put(&buf)

	n = 2 + 4 + 1 + len(b)
	_ = append(buf[:0],
		byte(n>>8),
		byte(n),
		byte(sid>>24),
		byte(sid>>16),
		byte(sid>>8),
		byte(sid),
		byte(ftype))
	if len(b) > 0 {
		_ = copy(buf[7:n], b)
	}

	return pc.Conn.Write(buf[0:n])
}

func (pc *pconn) WriteRaw(sid uint32, ftype byte, payload []byte) (n int, err error) {
	defer func() {
		if err != nil {
			pc.Close(proxy.GetErrorCode(err))
		} else if r := recover(); r != nil {
			pc.Close(0)
			fmt.Println("panic error")
		}
	}()
	if pc.closing {
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
			return
		}
		i += m
	}
	return
}

func (pc *pconn) WriteCode(sid uint32, ftype byte, code byte) {
	var payload []byte
	if code != 0 {
		payload = []byte{code}
	}
	pc.WriteRaw(sid, ftype, payload)
}

func (pc *pconn) Listen() {
	var err error
	defer func() {
		log.Printf("raw conn [%s] stop listening \n", pc.id[:4])
	}()
	for {
		select {
		case <-pc.ctx.Done():
			return
		default:
			if err = pc.ReadFrame(); err != nil {
				pc.Close(proxy.GetErrorCode(err))
				return
			}
		}
	}
}
func (pc *pconn) ReadFrame() (err error) {
	br := pc.br
	buf := *kit.Byte7.Get().(*[]byte)
	defer kit.Byte7.Put(&buf)

	if _, err = br.ReadFull(pc.Conn, buf[:7]); err != nil {
		return
	}
	n := binary.BigEndian.Uint16(buf[0:2])
	if n < HEADER_LEN {
		err = fmt.Errorf("frame too short")
	}
	sid := binary.BigEndian.Uint32(buf[2:6])
	ftype := buf[6]
	s := pc.Get(sid)
	n = n - 7
	if ftype == FRAME_CONNECT || ftype == FRAME_ASSOCIATE || ftype == FRAME_RELAY {
		if s == nil && n <= 128 {
			b := *kit.Byte128.Get().(*[]byte)
			defer kit.Byte128.Put(&b)

			if _, err = br.ReadFullWithTimeout(pc.Conn, b[0:n], Data_TimeOut); err != nil {
				return
			}

			var addr xnet.Addr
			if addr, err = xnet.ReadAddr(bytes.NewReader(b[:n])); err == nil {
				t := pc.p.NewTunnel(uuid.NewString(), nil, ftype, addr)
				t.Multiple = true
				s = pc.NewStream(sid, t)
				t.Src = s
				t.SrcPeer = pc.p
				go pc.Do(t)
			}
			n = 0
		}
	} else if s != nil {
		if ftype == FRAME_DATA {
			var m, i uint16
			for i = 0; i < n; {
				m = Frame_Buf_Size
				if i+m > n {
					m = n - i
				}
				payload := PayloadPool.Get().(*[]byte)
				if _, err = br.ReadFullWithTimeout(pc.Conn, (*payload)[0:m], Data_TimeOut); err != nil {
					return err
				}
				i += m
				if !s.closing {
					f := Frames.New(ftype, sid, FRAME_DATA)
					f.payload = payload
					f.size = m
					s.frmCh <- f
				}
			}
		} else {
			var code byte
			if n > 0 {
				if _, err = br.ReadFull(pc.Conn, buf[:n]); err != nil {
					return err
				}
				code = buf[0]
			}
			if !s.closing {
				s.frmCh <- Frames.New(ftype, sid, code)
			}
		}
		n = 0
	}
	if n > 0 {
		log.Printf("stream [%d] not exist, discard frame ftype %d   %d byte\n", sid, ftype, n)
		if _, err = io.CopyN(ioutil.Discard, pc.Conn, int64(n)); err != nil {
			return err
		}
	}
	return
}

type stremmCtrl struct {
	t     *proxy.Tunnel
	frmCh chan *frame
	pr    *io.PipeReader
	pw    *io.PipeWriter
}

type stream struct {
	*stremmCtrl
	net.Conn
	pc      *pconn
	id      uint32
	closing bool
	sent    bool
	closed  bool
	mu      sync.Mutex
}

func (s *stream) Read(b []byte) (n int, err error) {
	return s.pr.Read(b)
}

func (s *stream) Write(b []byte) (n int, err error) {
	return s.WriteRaw(FRAME_DATA, b)
}

func (s *stream) SetDeadline(t time.Time) error {
	return nil
}

func (s *stream) SetReadDeadline(t time.Time) error {
	return nil
}

func (s *stream) SetWriteDeadline(t time.Time) error {
	return nil
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
		f := <-s.frmCh
		if f == nil {
			return
		}
		s.handleFrame(f)
		if s.closing {
			return
		}
	}
}

func (s *stream) Close() error {
	s.markClose(nil, 0, true)
	return nil
}

func (s *stream) MarkClose() {
	s.markClose(nil, 0, true)
}

func (s *stream) MarkError(err error) {
	s.markClose(err, 0, true)
}

func (s *stream) MarkCode(code byte) {
	s.markClose(nil, code, true)
}

func (s *stream) SendBye() {
	s.markClose(nil, 0, true)
	s.WriteCode(FRAME_CLOSE, 0)
}

func (s *stream) SendRST(code byte) {
	s.markClose(nil, code, true)
	s.WriteCode(FRAME_RST, code)
}

func (s *stream) SendErrorMark(err error) {
	s.markClose(err, 0, true)
	s.WriteCode(FRAME_RST, proxy.GetErrorCode(err))
}

func (s *stream) markClose(err error, code byte, releaseFlag bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closing {
		return
	}

	s.closing = true
	// close(s.frmCh)

	// s.pr.Close()
	// s.pw.Close()
	if err != nil {
		if cd := proxy.GetErrorCode(err); cd != proxy.ERR_UNKNOWN {
			err = fmt.Errorf("error code %d  %s", cd, proxy.ErrTip[cd])
		}
		s.t.AddError(err)
	}

	if releaseFlag {
		s.pc.MarkReleaseStream(s)
	}
}
func (s *stream) WriteCode(ftype byte, code byte) {
	if s.sent {
		return
	}
	s.sent = true
	s.pc.WriteCode(s.id, ftype, code)
}

func (s *stream) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true
	close(s.frmCh)
	if s.t.Dst != nil && s.t.Dst != s {
		s.t.Dst.Close()
	}

	if s.t.Src != nil && s.t.Src != s {
		s.t.Src.Close()
	}

	s.pr.Close()
	s.pw.Close()
	// log.Printf("stream [%d] removed (%s)\n", s.id, s.t.Addr)
	s.frmCh = nil
	// s.stremmCtrl = nil
}

func (s *stream) handleFrame(f *frame) (err error) {
	switch f.ftype {
	case FRAME_DATA:
		buf := *f.payload
		if _, err = s.pw.Write(buf[:f.size]); err != nil {
			s.SendErrorMark(err)
		}
	case FRAME_FINISH: // half close
	case FRAME_CLOSE:
		s.MarkClose()
	case FRAME_RST:
		s.MarkCode(f.code)
	default:
		s.SendRST(proxy.ERR_COMMAND_UNKNOWN)
	}

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
	if s, ok := t.Dst.(*stream); ok {
		_, err = s.WriteRaw(t.Method, t.Addr)
	}
	return
}

func (l Local) AfterSend(t *proxy.Tunnel) (err error) {
	if s, ok := t.Dst.(*stream); ok {
		if s.closing {
			return
		}
		if t.WriteDstErr != nil {
			err = t.WriteDstErr
			s.MarkError(err)
		} else {
			s.Finish()
		}
	}
	return err
}

func (l Local) AfterReceive(t *proxy.Tunnel) (err error) {
	if s, ok := t.Dst.(*stream); ok {
		// if s.closing {
		// 	return
		// }

		if t.ReadDstErr != nil && t.ReadDstErr != io.EOF {
			s.MarkError(t.ReadDstErr)
		} else {
			if t.WriteSrcErr != nil {
				err = t.WriteSrcErr
				s.MarkError(err)
			}
			s.SendBye()
		}
	}
	return
}

type Remote struct{ pong }

func (r Remote) AfterDial(t *proxy.Tunnel, err error) error {
	if s, ok := t.Src.(*stream); ok {
		if err != nil {
			s.SendErrorMark(err)
		}
	}

	return err
}

func (r Remote) AfterSend(t *proxy.Tunnel) (err error) {
	if s, ok := t.Src.(*stream); ok {
		if s.closing {
			return
		}
		if t.ReadSrcErr != nil && t.ReadSrcErr != io.EOF {
			err = t.ReadSrcErr
			s.MarkError(err)
		} else if t.WriteDstErr != nil {
			err = t.WriteDstErr
			s.SendErrorMark(err)
		}
	}
	return
}

func (r Remote) AfterReceive(t *proxy.Tunnel) (err error) {
	if s, ok := t.Src.(*stream); ok {
		if s.closing {
			return
		}

		if t.ReadDstErr != nil && t.ReadDstErr != io.EOF {
			err = s.t.ReadDstErr
			s.SendErrorMark(err)
		} else if t.WriteSrcErr != nil {
			s.MarkClose()
		} else {
			s.SendBye()
		}
	}
	return
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
