package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"time"

	"github.com/miekg/dns"

	"github.com/pingworlds/pong/event"
	"github.com/pingworlds/pong/rule"
	"github.com/pingworlds/pong/xnet"
)

var Break_Time = 1 * time.Minute
var locs *container
var locCtrl *localCtrl
var dns_muu = 520
var directPeer *Proto

func init() {
	sp := event.NewProvider(100, context.Background())
	cp := event.NewProvider(100, context.Background())
	locCtrl = &localCtrl{ctrl: newCtrl(sp, cp, true)}

	directPeer = locCtrl.NewProto(xnet.Point{Transport: "tcp", Protocol: "direct"})
	directPeer.IsDirect = true
	directPeer.DialAddr = DialNetAddr
	locs = newContainer(locCtrl)
	locs.isLocal = true
	cp.Start()
	sp.Start()
}

func LocalDo(tunnel *Tunnel) (err error) {
	return locCtrl.Do(tunnel)
}

func NewLocalProto(point xnet.Point) *Proto {
	return locs.NewProto(point)
}

func SubLocalServiceEvent(s event.Stream) string {
	return locs.SubServiceEvent(s)
}

func CancelLocalServiceEvent(id string) {
	locs.CancelServiceEvent(id)
}

func SubLocalTunnelEvent(s event.Stream) string {
	return locs.SubTunnelEvent(s)
}

func CancelLocalTunnelEvent(id string) {
	locs.CancelTunnelEvent(id)
}

func StartLocal() {
	locs.Start()
}

func OrderPoints() {
	CheckPoints(cfg.Points, nil)
	sort.Slice(cfg.Points, func(i, j int) bool {
		return cfg.Points[i].Latency < cfg.Points[j].Latency
	})
}

func StopLocal() {
	locs.Stop()
	rule.AutoTryList.Save()
}

func ClearLocalTunnels() {
	locs.ClearTunnels()
}

func CloseLocalTunnel(id string) {
	locs.CloseTunnel(id)
}

func LocalStat() *Stat {
	return locs.Stat()
}

func LocalStatTunnel() []*TunnelData {
	return locs.StatTunnels()
}

var one_bytes = []byte{0x00}

func DnsLocalQuery(b []byte) (p []byte) {
	domain := ParseQueryDomain(b)
	if len(domain) > 0 {
		p, _ = DohQuery(b, true)
	}

	if p == nil || len(p) > dns_muu {
		p = one_bytes
	}
	return
}

func DnsRemoteQuery(b []byte) (p []byte) {
	domain := ParseQueryDomain(b)
	if len(domain) > 0 && rule.How(xnet.DOMAIN, domain) == rule.MODE_PROXY {
		p, _ = DohQuery(b, false)
	}
	if p == nil || len(p) > dns_muu {
		p = one_bytes
	}
	return
}

func DnsMustQuery(b []byte) (p []byte) {
	domain := ParseQueryDomain(b)
	if len(domain) == 0 {
		p = one_bytes
		return
	}
	isDirect := true
	if rule.How(xnet.DOMAIN, domain) == rule.MODE_PROXY {
		isDirect = false
	}
	p, _ = DohQuery(b, isDirect)
	if p == nil || len(p) > dns_muu {
		p = one_bytes
	}
	return p
}

func ParseQueryDomain(b []byte) (domain string) {
	var err error
	q := dns.Msg{}
	if err = q.Unpack(b); err != nil {
		return
	}
	if len(q.Question) <= 0 {
		return
	}

	name := q.Question[0].Name
	if len(name) < 1 {
		return
	}
	domain = name[:(len(name) - 1)]
	return
}

type localCtrl struct {
	*ctrl
}

func (c *localCtrl) NewProto(point xnet.Point) *Proto {
	p := c.ctrl.newProto(point)
	p.ctrl = c
	return p
}

//override
func (c *localCtrl) BeforeStart() {
	c.peers[directPeer.ID()] = directPeer
}

func (c *localCtrl) AfterStart() {
	if cfg.AutoOrderPoint {
		go OrderPoints()
	}
	go rule.Init(false)
	InitDoh()
}

func (c *localCtrl) NewPeer(point xnet.Point) (p Peer, err error) {
	var ptc *Protocol
	if ptc, err = GetProtocol(point.Protocol); err != nil {
		return
	}
	proto := c.NewProto(point)
	p = ptc.LocalFn(proto)
	proto.Do = locCtrl.Do
	return p, nil
}

func (c *localCtrl) NewListenPeer(point xnet.Point) (p Peer, err error) {
	return c.NewPeer(point)
}

func (l *localCtrl) Do(t *Tunnel) (err error) {
	defer func() {
		if err != nil && err != io.EOF {
			t.AddError(err, "")
		}
		t.Close()
	}()
	switch t.Method {
	case CONNECT:
		err = l.onConnect(t)
	default:
		err = fmt.Errorf("not support method %x", t.Method)
	}
	return
}

func (l *localCtrl) onConnect(t *Tunnel) (err error) {
	var f Filter
	host, _ := t.Addr.HostAndPort()
	mode := rule.How((t.Addr)[0], host)
	tryProxy := false
	// log.Printf("pass mode: %s   %d", host, mode)
	t.PassMode = mode

	switch mode {
	case rule.MODE_REJECT:
		err = fmt.Errorf("rule rejected")
		return
	case rule.MODE_DIRECT:
		if err = directPeer.Open(t); err != nil {
			log.Println(err)
			if cfg.AutoTry && rule.CanAutoTry(err.Error()) {
				t.AddError(err, "")
				err = nil
				tryProxy = true
				f, err = l.Open(t)
			}
		} else {
			f = EmptyFilter
		}
	case rule.MODE_PROXY:
		f, err = l.Open(t)
	}
	if err != nil {
		return
	}

	err = l.relay(f, t)
	if err == nil && tryProxy {
		// log.Printf("auto try.....mode:%d  %s ", mode, host)
		t.AutoTry = true
		rule.AddToAutoList((t.Addr)[0], host)
	}
	return
}

func (l *localCtrl) Open(t *Tunnel) (f Filter, err error) {
	if len(cfg.Points) == 0 {
		err = xnet.Err_NoUsefulPoint
		return
	}

	tt := time.Now().UnixMilli()

	for _, point := range cfg.Points {
		if point.Disabled || point.Breaking && time.Duration(tt-point.BreakTime) > Break_Time {
			continue
		}
		var peer Peer

		p := l.GetPeer(point.ID())
		if p == nil {
			if peer, err = l.NewPeer(*point); err != nil {
				log.Println("init peer error ", err)
				continue
			}
			l.PutPeer(point.ID(), peer)
		} else {
			peer = p
		}
		if err = peer.Open(t); err == nil {
			f = peer
			if point.Breaking {
				point.Breaking = false
				point.BreakTime = 0
			}
			return
		} else {
			point.Breaking = true
			point.BreakTime = tt
			log.Println(err)
		}
	}

	if f == nil {
		err = xnet.Err_NoUsefulPoint
		l.OnError(err)
	}
	return
}
