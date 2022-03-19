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
	go pg.start()
	return pg
}

type closeReq struct {
	stype byte
	pconn *pconn
	sid   uint32
}

type connPool struct {
	id      uint32
	conns   []*pconn
	closeCh chan closeReq
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
}

func (cp *connPool) New(p *pong, conn net.Conn, ver byte, do proxy.Do, isLocal bool) *pconn {
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
	cp.mu.Lock()
	cp.conns = append(cp.conns, pc)
	cp.mu.Unlock()
	return pc
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

	err := fmt.Errorf(proxy.ErrTip[proxy.ERR_FORCE_CLOSE])
	for _, c := range cp.conns {
		c.Stop(err)
	}
	cp.conns = []*pconn{}
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
			} else if req.stype == 1 {
				cp.removeConn(req.pconn)
			} else if req.stype == 2 {
				req.pconn.RemoveStream(req.sid)
			}
		}
	}
}

func (cp *connPool) checkTimeout() {
	t1 := time.Now().UnixMilli()
	for _, pc := range cp.conns {
		if pc == nil || pc.closing {
			continue
		}
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
					pc.Close()
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
	pc := p.NewConn(src, ver, p.Do, false)
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

		pc = p.NewConn(rc, VER, proxy.LocalDo, true)
		go pc.Listen()
	}
	if pc != nil {
		pc.OpenStream(t)
	} else {
		err = fmt.Errorf("open stream failed")
	}
	return
}

func (p *pong) NewConn(conn net.Conn, ver byte, do proxy.Do, isLocal bool) *pconn {
	return p.New(p, conn, ver, do, isLocal)
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
	err      error
}

func (pc *pconn) GetStream(sid uint32) *stream {
	pc.smu.Lock()
	defer pc.smu.Unlock()
	return pc.streams[sid]
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

func (pc *pconn) new(id uint32, t *proxy.Tunnel) *stream {
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
	pc.smu.Lock()
	if id == 0 {
		s.id = pc.n
		pc.n += 2
	}
	pc.streams[s.id] = s
	pc.smu.Unlock()
	go s.Listen()
	// log.Printf("stream [%d] created (%s) \n", s.id, t.Addr)
	return s
}

func (pc *pconn) RemoveStream(sid uint32) {
	pc.smu.Lock()
	defer pc.smu.Unlock()

	delete(pc.streams, sid)
	// log.Printf("stream [%d] removed  \n", sid)
}

var FrameWriteBuffer = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, Frame_Buf_Size+7)
		return &buf
	},
}

func (pc *pconn) MarkRelease(s *stream) {
	pc.p.closeCh <- closeReq{stype: 2, pconn: pc, sid: s.id}
}

func (pc *pconn) Close() {
	pc.doStop(nil, true)
}

func (pc *pconn) CloseWithError(err error) {
	pc.doStop(err, true)
}

func (pc *pconn) Stop(err error) {
	pc.doStop(err, false)
}

func (pc *pconn) doStop(err error, releaseFlag bool) {
	pc.smu.Lock()
	defer pc.smu.Unlock()

	if pc.closing {
		return
	}
	pc.old = true
	pc.closing = true
	pc.err = err

	pc.release()
	if releaseFlag {
		pc.p.closeCh <- closeReq{stype: 1, pconn: pc}
	}
}

func (pc *pconn) release() {
	for _, s := range pc.streams {
		s.Stop(pc.err)
		delete(pc.streams, s.id)
	}

	pc.br.Break = true //stop I/O
	pc.cancel()        //cancel listening
	pc.Conn.Close()    //close network c
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
			pc.CloseWithError(err)
		}
		// if r := recover(); r != nil {
		// 	pc.Close()
		// 	fmt.Println("panic error")
		// }
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
	for {
		select {
		case <-pc.ctx.Done():
			return
		default:
			if err = pc.ReadFrame(); err != nil {
				pc.CloseWithError(err)
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
	s := pc.GetStream(sid)

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
	s.doStop(nil, true)
	return nil
}

func (s *stream) Stop(err error) {
	s.doStop(err, false)
}

func (s *stream) CloseWithError(err error) {
	s.doStop(err, true)
}

func (s *stream) CloseWithCode(code byte) {
	var err error
	if code > 0 {
		err = fmt.Errorf(proxy.ErrTip[code])
	}
	s.doStop(err, true)
}

func (s *stream) SendBye() {
	s.WriteCode(FRAME_CLOSE, 0)
}

func (s *stream) SendRST(code byte) {
	s.WriteCode(FRAME_RST, code)
}

func (s *stream) SendError(err error) {
	s.WriteCode(FRAME_RST, proxy.GetErrorCode(err))
}

func (s *stream) doStop(err error, releaseFlag bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closing {
		return
	}
	s.closing = true
	close(s.frmCh)

	if s.t.Src != nil {
		s.t.Src.SetDeadline(time.Now())
	}
	if s.t.Dst != nil {
		s.t.Dst.SetDeadline(time.Now())
	}
	if releaseFlag {
		s.pc.MarkRelease(s)
	}
	s.pr.CloseWithError(err)
	s.pw.CloseWithError(err)
	// log.Printf("stream [%d] released (%s)\n", s.id, s.t.Addr)
}

func (s *stream) WriteCode(ftype byte, code byte) {
	if s.sent {
		return
	}
	s.sent = true
	s.pc.WriteCode(s.id, ftype, code)
}

func (s *stream) handleFrame(f *frame) {
	switch f.ftype {
	case FRAME_DATA:
		buf := *f.payload
		if _, err := s.pw.Write(buf[:f.size]); err != nil {
			s.SendError(err)
			s.CloseWithError(err)
		}
	case FRAME_FINISH: // half close
		s.pw.CloseWithError(io.EOF)
	case FRAME_CLOSE:
		s.Close()
	case FRAME_RST:
		s.CloseWithCode(f.code)
	default:
		s.SendRST(proxy.ERR_COMMAND_UNKNOWN)
		s.CloseWithCode(proxy.ERR_COMMAND_UNKNOWN)
	}

	Frames.Put(f)
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
		if _, err = s.WriteRaw(t.Method, t.Addr); err != nil {
			s.CloseWithError(err)
		}
	}
	return
}

func (l Local) AfterSend(t *proxy.Tunnel) (err error) {
	if s, ok := t.Dst.(*stream); ok && !t.RecvDone {
		if t.WriteDstErr != nil {
			err = t.WriteDstErr
			s.CloseWithError(err)
		} else {
			if t.ReadSrcErr != nil && proxy.GetErrorCode(err) == proxy.ERR_SRC_ABORT {
				s.SendRST(proxy.ERR_SRC_ABORT)
				s.CloseWithError(err)
			} else {
				s.Finish()
			}
		}
		// log.Printf("after send stream [%d](%s) %v\n", s.id, t.Addr, err)
	}
	return err
}

func (l Local) AfterReceive(t *proxy.Tunnel) (err error) {
	if s, ok := t.Dst.(*stream); ok {
		if t.ReadDstErr != nil && t.ReadDstErr != io.EOF {
			s.CloseWithError(t.ReadDstErr)
		} else {
			if t.WriteSrcErr != nil && proxy.GetErrorCode(t.WriteSrcErr) == proxy.ERR_SRC_ABORT {
				err = t.WriteSrcErr
			}
			s.SendBye()
			s.CloseWithError(err)
		}

		// log.Printf("after receive stream [%d](%s) %v\n", s.id, t.Addr, err)

	}
	return
}

type Remote struct{ pong }

func (r Remote) AfterDial(t *proxy.Tunnel, err error) error {
	if s, ok := t.Src.(*stream); ok && err != nil {
		s.SendError(err)
		s.CloseWithError(err)
	}
	return err
}

func (r Remote) AfterSend(t *proxy.Tunnel) (err error) {
	if s, ok := t.Src.(*stream); ok && !t.RecvDone {
		if t.ReadSrcErr != nil && t.ReadSrcErr != io.EOF {
			err = t.ReadSrcErr
		} else if t.WriteDstErr != nil {
			err = t.WriteDstErr
			s.SendError(err)
		}
		// log.Printf("after send stream [%d](%s) %v\n", s.id, t.Addr, err)
		s.CloseWithError(err)
	}
	return
}

func (r Remote) AfterReceive(t *proxy.Tunnel) (err error) {
	if s, ok := t.Src.(*stream); ok {
		if t.ReadDstErr != nil && t.ReadDstErr != io.EOF {
			err = s.t.ReadDstErr
			s.SendError(err)
		} else {
			s.SendBye()
		}
		// log.Printf("after receive stream [%d](%s) %v\n", s.id, t.Addr, err)
		s.CloseWithError(err)
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
