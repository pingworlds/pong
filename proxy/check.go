package proxy

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pingworlds/pong/event"
	"github.com/pingworlds/pong/xnet"

	"github.com/miekg/dns"
)

var GOOGLE = xnet.DomainToAddr("google.com", 443)

type pointCheckData struct {
	Id      string
	Host    string
	Latency int
	Error   string
}

func CheckPoints(points []*xnet.Point, notify event.Notify) {
	var wg sync.WaitGroup
	wg.Add(len(points))
	for _, point := range points {
		go func(p xnet.Point) {
			defer wg.Done()
			checkPoint(p, notify)
		}(*point)
	}
	wg.Wait()
}

func checkPoint(p xnet.Point, notify event.Notify) {
	var err error
	var peer Peer

	t1 := time.Now().UnixMilli()
	if peer, err = locs.NewPeer(p); err == nil {
		tun := locCtrl.NewTunnel(uuid.NewString(), nil, CONNECT, GOOGLE)
		defer tun.Close()
		if err = peer.Open(tun); err == nil {
			p.Latency = int(time.Now().UnixMilli() - t1)
			tun.Dst.Close()
		} else {
			p.Latency = 9999
		}
	}
	pd := pointCheckData{Id: p.ID(), Host: p.Host, Latency: p.Latency}
	if err != nil {
		pd.Error = err.Error()
	}
	if notify != nil {
		notify(event.NewEvent(event.DATA, pd, nil))
	}
}

func pack(domain string) ([]byte, error) {
	q := dns.Msg{}
	q.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	return q.Pack()
}

type dohData struct {
	Path   string
	Time   int
	Result string
	Error  string
}

func CheckDohs(isDirect bool, dohs []*xnet.Doh, domain string, notify event.Notify) {
	var wg sync.WaitGroup
	b, err := pack(domain)
	if err != nil {
		if notify != nil {
			notify(event.NewEvent(event.DATA, nil, err))
		}
		return
	}

	wg.Add(len(dohs))
	for _, doh := range dohs {
		go func(d *xnet.Doh) {
			defer wg.Done()
			var s string
			var latency = 9999
			client, err := NewDohClient(d)

			if err == nil {
				s, err = checkDohClient(isDirect, client, b)
				latency = client.Latency
			}
			data := dohData{
				Path:   d.Path,
				Time:   latency,
				Result: s}
			if err != nil {
				data.Error = err.Error()
			}

			if notify != nil {
				notify(event.NewEvent(event.DATA, data, nil))
			}
		}(doh)
	}
	wg.Wait()
}

func CheckDohClients(isDirect bool, clients []*DohClient, domain string) {
	var wg sync.WaitGroup
	b, err := pack(domain)
	if err != nil {
		return
	}

	wg.Add(len(clients))
	for _, client := range clients {
		go func(c *DohClient) {
			defer wg.Done()
			checkDohClient(isDirect, c, b)
		}(client)
	}
	wg.Wait()
}

func checkDohClient(isDirect bool, client *DohClient, b []byte) (s string, err error) {
	t1 := time.Now().UnixMilli()
	p, err := client.Query(b, isDirect)
	if err != nil {
		client.Latency = 9999
		return
	}
	client.Latency = int(time.Now().UnixMilli() - t1)
	dm := dns.Msg{}
	dm.Unpack(p)
	if len(dm.Answer) > 0 {
		s = dm.Answer[0].String()
	}
	return
}
