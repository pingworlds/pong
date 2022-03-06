package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
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
	Errors       []string
	closed       bool
	TunnelEventNotifier
}

func (t *Tunnel) AddError(err error, msg string) {
	if err != nil {
		arr := strings.Split(err.Error(), ":")
		msg = strings.Trim(arr[len(arr)-1]+msg, " ")
	}
	t.Errors = append(t.Errors, msg)
}

func (t *Tunnel) IsLocal() bool {
	return t.ctrl.isLocal
}

func (t *Tunnel) CloseWithError(err error) {
	t.AddError(err, "")
	t.Close()
}

func (t *Tunnel) Close() {
	if t.closed {
		return
	}
	t.closed = true
	t.ctrl.RemoveTunnel(t.Id)
	if t.Src != nil {
		t.Src.Close()
	}
	if t.Dst != nil {
		t.Dst.Close()
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
	t.OnTunnelClose(t)
}

//protocol filter
type Filter interface {
	BeforeDial(t *Tunnel, err error) error
	AfterDial(t *Tunnel, err error) error
	BeforeSend(t *Tunnel) error
	AfterSend(t *Tunnel, err error) error
	BeforeReceive(t *Tunnel) error
	AfterReceive(t *Tunnel, err error) error
}

//dial remote peer open a tunnel for target address
type Dialer interface {
	Open(t *Tunnel) error
}

type DialAddr func(point xnet.Point, addr xnet.Addr) (net.Conn, error)

func DialPeerAddr(point xnet.Point, addr xnet.Addr) (net.Conn, error) {
	return transport.Dial(point)
}

func DialNetAddr(point xnet.Point, addr xnet.Addr) (net.Conn, error) {
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
	xnet.Point
	filter
	Do Do
	DialAddr
	ServiceEventNotifier
	TunnelEventNotifier
	IsDirect bool
	cn       int
	ctrl     controller
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
	log.Printf("empty server")
}

func (p *Proto) Stat() *PointStat {
	return NewPointStat(p, p.cn, p.cn)
}

func (p *Proto) Close() error {
	return nil
}

func (p *Proto) ClearConn() {

}

type controller interface {
	ServiceEventNotifier
	TunnelEventNotifier
	BeforeStart()
	AfterStart()
	CancelAllSub()
	NewProto(point xnet.Point) *Proto
	NewListenPeer(point xnet.Point) (p Peer, err error)
	NewPeer(point xnet.Point) (p Peer, err error)
	PutPeer(id string, peer Peer)
	RemovePeer(id string)
	NewTunnel(id string, src net.Conn, method byte, addr xnet.Addr) *Tunnel
	ClearTunnels()
	ClearPeers()
	CloseTunnel(id string)
	RemoveTunnel(id string)
	Stat() *Stat
	StatTunnels() []*TunnelData
	LogStatus(st *Stat)
}

func newCtrl(sp event.Provider, cp event.Provider, isLocal bool) *ctrl {
	return &ctrl{
		sp:      sp,
		cp:      cp,
		peers:   map[string]Peer{},
		tunnels: map[string]*Tunnel{},
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

func (c *ctrl) RemoveTunnel(id string) {
	c.tmu.Lock()
	defer c.tmu.Unlock()
	delete(c.tunnels, id)
}

func (c *ctrl) CloseTunnel(id string) {
	err := fmt.Errorf("forceibly closed manually")
	if t, ok := c.tunnels[id]; ok {
		t.CloseWithError(err)
	}
}

func (c *ctrl) ClearTunnels() {
	err := fmt.Errorf("forceibly closed manually")
	for _, t := range c.tunnels {
		t.CloseWithError(err)
	}
	for _, p := range c.peers {
		p.ClearConn()
	}
}
func (c *ctrl) PutPeer(id string, peer Peer) {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	c.peers[id] = peer
}
func (c *ctrl) GetPeer(id string) Peer {
	return c.peers[id]
}

func (c *ctrl) RemovePeer(id string) {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	delete(c.peers, id)
}
func (c *ctrl) ClearPeers() {
	for _, p := range c.peers {
		p.Close()
	}
}
func (c *ctrl) BeforeStart() {}
func (c *ctrl) AfterStart()  {}

func (c *ctrl) newProto(point xnet.Point) *Proto {
	return &Proto{
		Point:                point,
		ServiceEventNotifier: c,
		TunnelEventNotifier:  c,
		DialAddr:             DialPeerAddr,
	}
}

func (c *ctrl) NewTunnel(id string, src net.Conn, method byte, addr xnet.Addr) *Tunnel {
	t := &Tunnel{StartTime: time.Now().UnixMilli(),
		Id:                  id,
		Method:              method,
		Addr:                addr,
		Src:                 src,
		Errors:              []string{},
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
	fmt.Fprintf(w, "-------------------------------------------------------------------------\n")
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

func (c *ctrl) relay(f Filter, t *Tunnel) (err error) {
	defer func() {
		t.Dst.Close()
		if r := recover(); r != nil {
			fmt.Println("panic error")
		}
	}()

	if err = f.BeforeSend(t); err != nil {
		return
	}

	t1 := time.Now().UnixMilli()
	t.Connected = true

	go func() {
		n, err := io.Copy(t.Dst, t.Src)
		// log.Printf("src    ->    dst  %d  bytes\n", n)
		f.AfterSend(t, err)
		// checkErrors(t, err)
		t.Sent = n
		t.SentDuration = time.Now().UnixMilli() - t1
		c.OnTunnelSent(t)
	}()

	if err = f.BeforeReceive(t); err == nil {
		t.Received, err = io.Copy(t.Src, t.Dst)
		// log.Printf("dst    ->    src  %d  bytes \n", t.Received)
		err = f.AfterReceive(t, err)
		// checkErrors(t, err)
		err = nil
	}

	t.RecvDuration = time.Now().UnixMilli() - t1
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
	c.BeforeStart()
	c.ctx, c.cancel = context.WithCancel(context.Background())
	t := cfg.StatTime
	if t == 0 {
		t = 10
	} else if t < 3 {
		t = 3
	}
	t = 3
	loopTime := t * time.Second
	c.OnStart()
	go func() {
		tiker := time.NewTicker(loopTime)
		autoTiker := time.NewTicker(3 * 60 * time.Second)
		var st *Stat

		defer func() {
			tiker.Stop()
			autoTiker.Stop()
		}()
		var i = 0
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-tiker.C:
				st = c.Stat()
				i++
				if i > 3 {
					i = 0
					c.LogStatus(st)
					log.Printf("coroutine number %d\n", runtime.NumGoroutine())
				}
			case <-autoTiker.C:
				if c.isLocal {
					rule.LazySave()
				} else if st != nil {
					c.LogStatus(st)
				}
				log.Printf("coroutine number %d\n", runtime.NumGoroutine())
			}
		}
	}()

	for _, point := range cfg.Listens {
		if point.Disabled {
			continue
		}
		go func(p xnet.Point) {
			if err := c.doListen(p); err != nil {
				log.Println(err)
				c.OnError(err)
				c.Stop()
			}
		}(*point)
	}
	c.AfterStart()
	<-c.ctx.Done()
}

func (c *container) doListen(p xnet.Point) (err error) {
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
		c.cancel = nil
	}
	c.ClearTunnels()
	c.ClearPeers()
	for _, svr := range c.svrs {
		svr.Close()
	}
	c.svrs = c.svrs[:0]
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
func (f filter) AfterSend(t *Tunnel, err error) error {
	return err
}
func (f filter) BeforeReceive(t *Tunnel) error {
	return nil
}
func (f filter) AfterReceive(t *Tunnel, err error) error {
	return err
}

var EmptyFilter = &filter{}

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

	if len(t.Errors) > 0 {
		ev["Errors"] = t.Errors[0]
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
