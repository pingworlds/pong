package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/pingworlds/pong/event"
	"github.com/pingworlds/pong/rule"
	"github.com/pingworlds/pong/xnet"
	"github.com/pingworlds/pong/xnet/transport"
)

const (
	CONNECT   byte = 1
	ASSOCIATE byte = 3
	UDP       byte = 5 //udp relay
	DNS       byte = 6 //dns query

	SERVICE_START byte = 1
	SERVICE_STOP  byte = 2
	SERVICE_DATA  byte = 3
	SERVICE_ERROR byte = 4
	SERVICE_FATAL byte = 5
	SERVICE_STAT  byte = 6

	TUNNEL_OPEN   byte = 1
	TUNNEL_SENT   byte = 2
	TUNNEL_RECIVE byte = 3
	TUNNEL_CLOSE  byte = 4
	TUNNEL_Error  byte = 5

	TunnelEvent_MAX int = 300
)

var Header_TimeOut int64 = 10 * 1000
var Copy_Read_Timeout int = 5 * 60 //second
var Copy_Read_Loop_Max = Copy_Read_Timeout / 45

var Err_Manually_Close = fmt.Errorf("forceibly closed manually")

type Tunnel struct {
	ctrl         *ctrl
	Id           string
	Method       byte
	Addr         xnet.Addr
	Src          net.Conn
	Dst          net.Conn
	SrcPeer      Peer
	DstPeer      Peer
	StartTime    int64
	OpenDuration int64
	Sent         int64
	SentDuration int64
	Received     int64
	RecvDuration int64
	CloseTime    int64
	PassMode     byte
	Connected    bool
	AutoTry      bool
	Multiple     bool
	Error        error
	Closed       bool
	SendDone     bool
	RecvDone     bool
	WriteSrcErr  error
	ReadSrcErr   error
	WriteDstErr  error
	ReadDstErr   error
	TunnelEventNotifier
}

func (t *Tunnel) AddError(err error) {
	if err == nil || err == io.EOF {
		return
	}
	t.Error = err
}

func (t *Tunnel) IsLocal() bool {
	return t.ctrl.isLocal
}

func (t *Tunnel) CloseWithError(err error) {
	if t.Release(err) {
		t.ctrl.RemoveTunnel(t.Id)
	}
}

func (t *Tunnel) Close() {
	t.CloseWithError(nil)
}

func (t *Tunnel) Release(err error) bool {
	if t.Closed {
		return false
	}
	t.Closed = true
	t.AddError(err)
	if t.Error != nil {
		log.Printf("connect %s error   %v", t.Addr, t.Error)
	}

	if t.Src != nil {
		t.Src.Close()
		// t.Src = nil
	}
	if t.Dst != nil {
		t.Dst.Close()
		// t.Dst = nil
	}
	if !t.Multiple {
		if t.DstPeer != nil {
			t.DstPeer.GetProto().cn--
		}
		if t.SrcPeer != nil {
			t.SrcPeer.GetProto().cn--
		}
	}
	t.CloseTime = time.Now().UnixMilli()
	// log.Printf("tunnel closed (%s)\n", t.Addr)
	t.OnTunnelClose(t)
	return true
}

//protocol filter
type Filter interface {
	BeforeDial(t *Tunnel, err error) error
	AfterDial(t *Tunnel, err error) error
	BeforeSend(t *Tunnel) error
	AfterSend(t *Tunnel) error
	BeforeReceive(t *Tunnel) error
	AfterReceive(t *Tunnel) error
}

//dial remote peer open a tunnel for target address
type Dialer interface {
	Open(t *Tunnel) error
}

type DialAddr func(point *xnet.Point, addr xnet.Addr) (net.Conn, error)

func DialPeerAddr(point *xnet.Point, addr xnet.Addr) (net.Conn, error) {
	return transport.Dial(point)
}

func DialNetAddr(point *xnet.Point, addr xnet.Addr) (net.Conn, error) {
	return xnet.Dial(addr)
}

type Do func(t *Tunnel) (err error)

//event type: start|close|error|fatal|data|stat
type ServiceEventNotifier interface {
	OnStart()
	OnStop(err error)
	OnError(err error)
	OnFatal(err error)
	SubServiceEvent(s event.Stream) string
	CancelServiceEvent(id string)
}

//event type: open|close|send|receive
type TunnelEventNotifier interface {
	OnTunnelOpen(t *Tunnel)
	OnTunnelClose(t *Tunnel)
	OnTunnelSent(t *Tunnel)
	OnTunnelReceived(t *Tunnel)
	SubTunnelEvent(s event.Stream) string
	CancelTunnelEvent(id string)
}

type Peer interface {
	xnet.IDCode
	Dialer
	Filter
	io.Closer
	transport.Handler
	ServiceEventNotifier
	TunnelEventNotifier
	GetProto() *Proto
	Stat() *PointStat
	ClearConn()
}

//all proxy protocol proto implement
type Proto struct {
	*xnet.Point
	Filter
	Do Do
	DialAddr
	ServiceEventNotifier
	TunnelEventNotifier
	IsDirect bool
	cn       int
	ctrl     controller
}

func (p *Proto) ReadFull(r io.Reader, b []byte) (n int, err error) {
	return xnet.ReadFullWithTimeout(r, b, Header_TimeOut)
}

func (p *Proto) GetProto() *Proto {
	return p
}

func (p *Proto) NewTunnel(id string, src net.Conn, method byte, addr xnet.Addr) *Tunnel {
	t := p.ctrl.NewTunnel(id, src, method, addr)
	t.SrcPeer = p
	p.cn++
	return t
}

func (p *Proto) Open(t *Tunnel) error {
	t1 := time.Now().UnixMilli()
	conn, err := p.DialAddr(p.Point, t.Addr)
	t.OpenDuration = time.Now().UnixMilli() - t1
	if err != nil {
		return err
	}

	t.Dst = conn
	t.DstPeer = p
	p.cn++
	p.OnTunnelOpen(t)
	return nil
}

func (p *Proto) Handle(conn net.Conn) {
	log.Printf("empty server\n")
}

func (p *Proto) Stat() *PointStat {
	return NewPointStat(p, p.cn, p.cn)
}

func (p *Proto) ClearConn() {
}

func (p *Proto) Close() error {
	return nil
}

type controller interface {
	ServiceEventNotifier
	TunnelEventNotifier
	BeforeStart()
	AfterStart()
	CancelAllSub()
	NewProto(point *xnet.Point) (proto *Proto, err error)
	NewListenPeer(point *xnet.Point) (p Peer, err error)
	NewPeer(point *xnet.Point) (p Peer, err error)
	PutPeer(id string, peer Peer)
	RemovePeer(id string)
	NewTunnel(id string, src net.Conn, method byte, addr xnet.Addr) *Tunnel
	ClearTunnels()
	ClearPeers()
	Reset()
	CloseTunnel(id string)
	RemoveTunnel(id string)
	Stat() *Stat
	StatTunnels() []*TunnelData
	LogStatus(st *Stat)
}

func newCtrl(sp event.Provider, cp event.Provider, isLocal bool) *ctrl {
	return &ctrl{
		sp: sp,
		cp: cp,
		// peers:   map[string]Peer{},
		// tunnels: map[string]*Tunnel{},
		isLocal: isLocal,
	}
}

type ctrl struct {
	peers   map[string]Peer
	tunnels map[string]*Tunnel
	sp      event.Provider
	cp      event.Provider
	isLocal bool
	tmu     sync.Mutex
	pmu     sync.Mutex
}

func (c *ctrl) Reset() {
	c.tmu.Lock()
	defer c.tmu.Unlock()

	c.peers = map[string]Peer{}
	c.tunnels = map[string]*Tunnel{}
}

func (c *ctrl) RemoveTunnel(id string) {
	c.tmu.Lock()
	defer c.tmu.Unlock()

	delete(c.tunnels, id)
}

func (c *ctrl) CloseTunnel(id string) {
	c.tmu.Lock()
	defer c.tmu.Unlock()

	if t, ok := c.tunnels[id]; ok {
		t.Release(Err_Manually_Close)
		delete(c.tunnels, id)
	}
}

func (c *ctrl) ClearTunnels() {
	c.tmu.Lock()
	defer c.tmu.Unlock()

	for _, t := range c.tunnels {
		t.Release(Err_Manually_Close)
		delete(c.tunnels, t.Id)
	}
}

func (c *ctrl) PutPeer(id string, peer Peer) {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	c.peers[id] = peer
}

func (c *ctrl) GetPeer(id string) Peer {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	return c.peers[id]
}

func (c *ctrl) RemovePeer(id string) {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	delete(c.peers, id)
}

func (c *ctrl) ClearPeers() {
	c.pmu.Lock()
	defer c.pmu.Unlock()

	log.Printf("clear %d  peers \n", len(c.peers))
	for _, p := range c.peers {
		p.Close()
		delete(c.peers, p.GetProto().Id)
	}
}

func (c *ctrl) BeforeStart() {}
func (c *ctrl) AfterStart()  {}

func (c *ctrl) newProto(point *xnet.Point) (proto *Proto, err error) {
	if point == nil {
		err = fmt.Errorf("null point")
		return
	}
	proto = &Proto{
		Point:                point,
		ServiceEventNotifier: c,
		TunnelEventNotifier:  c,
		DialAddr:             DialPeerAddr,
		Filter:               DefaultFilter,
	}
	return
}

func (c *ctrl) NewTunnel(id string, src net.Conn, method byte, addr xnet.Addr) *Tunnel {
	t := &Tunnel{StartTime: time.Now().UnixMilli(),
		Id:                  id,
		Method:              method,
		Addr:                addr,
		Src:                 src,
		TunnelEventNotifier: c,
		ctrl:                c}

	c.tmu.Lock()
	c.tunnels[t.Id] = t
	c.tmu.Unlock()
	return t
}

func (c *ctrl) StatTunnels() []*TunnelData {
	items := []*TunnelData{}
	if len(c.tunnels) < TunnelEvent_MAX {
		for _, t := range c.tunnels {
			items = append(items, NewTunnelData(t))
		}
	}
	return items
}

func (c *ctrl) Stat() *Stat {
	stats := []*PointStat{}
	n := 0
	m := 0

	for _, p := range c.peers {
		stat := p.Stat()
		if stat.ConnCount > 0 {
			stats = append(stats, stat)
			n += stat.ConnCount
			m += stat.StreamCount
		}
	}

	st := &Stat{
		ConnCount:   n,
		StreamCount: m,
		Points:      stats,
	}
	c.OnStat(st)
	return st
}

func (c *ctrl) LogStatus(st *Stat) {
	if st == nil {
		return
	}
	padding := 3
	w := tabwriter.NewWriter(os.Stdout, 0, 0, padding, ' ', 0)
	fmt.Fprintf(w, "\n-------------------------------------------------------------------------\n")
	fmt.Fprintf(w, "\tid\ttransport\tprotocol\tconnections\tstreams\n")
	for _, t := range st.Points {
		fmt.Fprintf(w, "\t%s\t%s\t%s\t%d\t%d\n", t.Code, t.Transport, t.Protocol, t.ConnCount, t.StreamCount)
	}
	fmt.Fprintf(w, "\t\t\ttotal\t%d\t%d \n", st.ConnCount, st.StreamCount)
	fmt.Fprintf(w, "-------------------------------------------------------------------------\n")
	w.Flush()
}

func (c *ctrl) SubServiceEvent(s event.Stream) string {
	return c.sp.Sub(s)
}
func (c *ctrl) CancelServiceEvent(id string) {
	c.sp.Cancel(id)
}

func (c *ctrl) SubTunnelEvent(s event.Stream) string {
	return c.cp.Sub(s)
}
func (c *ctrl) CancelTunnelEvent(id string) {
	c.cp.Cancel(id)
}

func (c *ctrl) CancelAllSub() {
	c.sp.CancelAll()
	c.cp.CancelAll()
}

func (c *ctrl) OnStart() {
	c.sp.Notify(event.NewEvent(SERVICE_START, nil, nil))
}

func (c *ctrl) OnStop(err error) {
	c.sp.Notify(event.NewEvent(SERVICE_STOP, nil, err))
}

func (c *ctrl) OnError(err error) {
	c.sp.Notify(event.NewEvent(SERVICE_ERROR, nil, err))
}

func (c *ctrl) OnFatal(err error) {
	c.sp.Notify(event.NewEvent(SERVICE_FATAL, nil, err))
}

func (c *ctrl) OnStat(st *Stat) {
	c.sp.Notify(event.NewEvent(SERVICE_STAT, st, nil))
}

func (c *ctrl) OnTunnelOpen(t *Tunnel) {
	if len(c.tunnels) > TunnelEvent_MAX {
		return
	}
	ev := NewTunnelData(t)
	(*ev)["Type"] = TUNNEL_OPEN
	c.cp.Notify(ev)
}

func (c *ctrl) OnTunnelClose(t *Tunnel) {
	if len(c.tunnels) > TunnelEvent_MAX {
		return
	}
	ev := NewTunnelData(t)
	(*ev)["Type"] = TUNNEL_CLOSE
	c.cp.Notify(ev)
}

func (c *ctrl) OnTunnelSent(t *Tunnel) {
	if len(c.tunnels) > TunnelEvent_MAX {
		return
	}
	ev := NewTunnelEvent(t)
	ev.Type = TUNNEL_SENT
	ev.Timestamp = time.Now().UnixMilli()
	ev.Duration = t.SentDuration
	ev.Bytes = t.Sent
	c.cp.Notify(ev)
}

func (c *ctrl) OnTunnelReceived(t *Tunnel) {
	if len(c.tunnels) > TunnelEvent_MAX {
		return
	}
	ev := NewTunnelEvent(t)
	ev.Type = TUNNEL_RECIVE
	ev.Timestamp = time.Now().UnixMilli()
	ev.Duration = t.RecvDuration
	ev.Bytes = t.Received
	c.cp.Notify(ev)
}

var errInvalidWrite = errors.New("invalid write result")

var CopyBuffers = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 8096)
		return &buf
	},
}

func NewFatalError(s string) FatalError {
	return FatalError{s: s}
}

type FatalError struct {
	s string
}

func (e FatalError) Error() string {
	return e.s
}

type WriteTimeOutError error

func Copy(dst net.Conn, src net.Conn, ctx context.Context, sendOrReceive bool) (written int64, er error, ew error) {
	buf := *CopyBuffers.Get().(*[]byte)
	defer CopyBuffers.Put(&buf)
	var nr, nw, count int

	for {
		select {
		case <-ctx.Done():
			return
		default:
			nr, er = src.Read(buf)
			if nr > 0 {
				nw, ew = dst.Write(buf[0:nr])
				if nw < 0 || nr < nw {
					nw = 0
					if ew == nil {
						ew = errInvalidWrite
					}
				}
				// if sendOrReceive {
				// 	log.Printf("   src ----->  dst %d bytes\n", nw)
				// } else {
				// 	log.Printf("   dst ----->  src %d bytes\n", nw)
				// }
				written += int64(nw)
				if ew != nil {
					return
				}
				if nr != nw {
					ew = io.ErrShortWrite
					return
				}
			}
			if er != nil {
				return
			}
			if nr == 0 {
				count++
				if count >= Copy_Read_Loop_Max {
					er = xnet.TimeoutError
					return
				}
			}
		}
	}
}

func (c *ctrl) relay(f Filter, t *Tunnel) (err error) {
	if err = f.BeforeSend(t); err != nil {
		return
	}

	t1 := time.Now().UnixMilli()
	t.Connected = true
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		t.Sent, t.ReadSrcErr, t.WriteDstErr = Copy(t.Dst, t.Src, ctx, true)
		t.SendDone = true
		if err = f.AfterSend(t); err != nil && !t.RecvDone {
			cancel()
		}
		// log.Printf("src    ->    dst  %d  bytes(%s)  %v\n", t.Sent, t.Addr, err)
		t.SentDuration = time.Now().UnixMilli() - t1
		c.OnTunnelSent(t)
	}()

	if err = f.BeforeReceive(t); err == nil {
		t.Received, t.ReadDstErr, t.WriteSrcErr = Copy(t.Src, t.Dst, ctx, false)
	}

	t.RecvDone = true
	t.RecvDuration = time.Now().UnixMilli() - t1
	cancel()
	f.AfterReceive(t)
	// log.Printf("dst    ->    src  %d  bytes (%s)  %v \n", t.Received, t.Addr, err)
	return
}

func newContainer(c controller) *container {
	return &container{controller: c, svrs: []transport.Server{}}
}

type container struct {
	controller
	ctx     context.Context
	cancel  context.CancelFunc
	svrs    []transport.Server
	running bool
	isLocal bool
}

func (c *container) IsRunning() bool {
	return c.running
}

func (c *container) Start() {
	if c.running {
		return
	}
	c.running = true
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.Reset()
	c.BeforeStart()

	go func() {
		t := cfg.StatTime
		if t == 0 {
			t = 10
		} else if t < 3 {
			t = 3
		}
		// t = 5
		loopTime := t * time.Second
		tiker := time.NewTicker(loopTime)
		autoTiker := time.NewTicker(5 * time.Minute)
		var st *Stat
		i := 0
		defer func() {
			tiker.Stop()
			autoTiker.Stop()
		}()

		for {
			select {
			case <-c.ctx.Done():
				return
			case <-tiker.C:
				st = c.Stat()
				i++
				if i > 30 {
					i = 0
					c.LogStatus(st)
					log.Printf("coroutine number %d\n", runtime.NumGoroutine())
				}
			case <-autoTiker.C:
				if c.isLocal {
					rule.LazySave()
				}
			}
		}
	}()
	c.OnStart()
	for _, point := range cfg.Listens {
		if point.Disabled {
			continue
		}
		go func(p *xnet.Point) {
			if err := c.doListen(p); err != nil {
				log.Println("listen error ", err)
				c.OnError(err)
				c.Stop()
			}
		}(point)
	}
	<-time.After(1 * time.Second)
	c.AfterStart()
	log.Printf("coroutine number %d\n", runtime.NumGoroutine())

	<-c.ctx.Done()
}

func (c *container) doListen(p *xnet.Point) (err error) {
	peer, err := c.NewListenPeer(p)
	if err != nil {
		return
	}
	defer peer.Close()
	if err != nil {
		err = fmt.Errorf("init  %s proxy server failed, %v", p.Protocol, err)
		return
	}

	var tsvr transport.Server
	tsvr, err = transport.NewServer(p.Transport)
	if err != nil {
		err = fmt.Errorf("start %s:%s server failed, %v", p.Transport, p.Host, err)
		return
	}
	c.svrs = append(c.svrs, tsvr)
	defer tsvr.Close()
	log.Printf("%s  %s server listen on %s \n", p.Protocol, p.Transport, p.Host)
	if err = tsvr.Listen(p, peer.Handle); err != nil {
		err = fmt.Errorf("%s  %s server listen on %s failed, %s", p.Protocol, p.Transport, p.Host, err.Error())
	}
	return
}

func (c *container) Stop() {
	if !c.running {
		return
	}
	c.running = false
	if c.cancel != nil {
		c.cancel()
	}
	for _, svr := range c.svrs {
		svr.Close()
	}
	c.svrs = c.svrs[:0]
	log.Println("container stoped")
	log.Printf("coroutine number %d\n", runtime.NumGoroutine())
}

type filter struct{}

func (f filter) BeforeDial(t *Tunnel, err error) error {
	return err
}
func (f filter) AfterDial(t *Tunnel, err error) error {
	return err
}
func (f filter) BeforeSend(t *Tunnel) error {
	return nil
}

func (f filter) AfterSend(t *Tunnel) error {
	return nil
}
func (f filter) BeforeReceive(t *Tunnel) error {
	return nil
}
func (f filter) AfterReceive(t *Tunnel) error {
	return nil
}

type defautFilter struct {
	*filter
}

func (f defautFilter) AfterSend(t *Tunnel) (err error) {
	if t.WriteDstErr != nil {
		err = t.WriteDstErr
	} else if !t.RecvDone && t.ReadSrcErr != nil && t.ReadSrcErr != io.EOF {
		err = t.ReadSrcErr
	}
	if err != nil {
		if t.Dst != nil {
			t.Dst.Close()
		}

		if t.Src != nil {
			t.Src.Close()
		}
	}
	return
}

func (f defautFilter) AfterReceive(t *Tunnel) (err error) {
	if t.WriteSrcErr != nil {
		err = t.WriteSrcErr
	} else if t.ReadDstErr != nil && t.ReadDstErr != io.EOF {
		err = t.ReadDstErr
	}
	if t.Dst != nil {
		t.Dst.Close()
	}

	if t.Src != nil {
		t.Src.Close()
	}
	return
}

var EmptyFilter = &filter{}

var DefaultFilter = &defautFilter{filter: EmptyFilter}

type Create func(p *Proto) Peer

type Protocol struct {
	Name     string
	LocalFn  Create
	RemoteFn Create
}

var mu sync.Mutex
var protocols = make(map[string]*Protocol)

func RegProtocol(name string, p *Protocol) error {
	mu.Lock()
	defer mu.Unlock()
	protocols[name] = p
	return nil
}
func GetProtocol(name string) (ptc *Protocol, err error) {
	ptc = protocols[name]
	if ptc == nil {
		err = fmt.Errorf("not support protocol %s", name)
	}
	return
}

func NewTunnelEvent(t *Tunnel) *TunnelEvent {
	return &TunnelEvent{Id: t.Id}
}

func NewTunnelData(t *Tunnel) *TunnelData {
	host, port := t.Addr.HostAndPort()
	ev := TunnelData{
		"Host":         host,
		"Port":         port,
		"Id":           t.Id,
		"StartTime":    t.StartTime,
		"OpenDuration": t.OpenDuration,
		"Sent":         t.Sent,
		"SentDuration": t.SentDuration,
		"Received":     t.Received,
		"RecvDuration": t.RecvDuration,
		"CloseTime":    t.CloseTime,
		"PassMode":     t.PassMode,
		"Connected":    t.Connected,
		"AutoTry":      t.AutoTry,
	}
	if t.Error != nil {
		ev["Error"] = t.Error.Error()
	}
	peer := t.SrcPeer
	if t.IsLocal() {
		peer = t.DstPeer
	}
	if peer != nil {
		p := peer.GetProto()
		ev["Transport"] = p.Transport
		ev["Protocol"] = p.Protocol
		ev["PointId"] = p.ID()
		ev["IsDirect"] = p.IsDirect
	}
	return &ev
}

type TunnelData map[string]interface{}

type TunnelEvent struct {
	Type      byte
	Id        string
	Bytes     int64
	Duration  int64
	Error     string
	Timestamp int64
}

type Stat struct {
	ConnCount   int
	StreamCount int
	Points      []*PointStat
}

type PointStat struct {
	Id          string
	Code        string
	Name        string
	Transport   string
	Protocol    string
	ConnCount   int
	StreamCount int
	IsDirect    bool
}

func NewPointStat(p *Proto, connCount, streamCount int) *PointStat {
	return &PointStat{
		IsDirect:    p.IsDirect,
		Id:          p.ID(),
		Code:        p.Code(),
		Name:        p.Name,
		Transport:   p.Transport,
		Protocol:    p.Protocol,
		ConnCount:   connCount,
		StreamCount: streamCount,
	}
}
