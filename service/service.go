package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pingworlds/pong/config"
	"github.com/pingworlds/pong/event"
	"github.com/pingworlds/pong/proxy"
	"github.com/pingworlds/pong/rule"
	"github.com/pingworlds/pong/xnet"

	_ "github.com/pingworlds/pong/proxy/pong"
	_ "github.com/pingworlds/pong/proxy/socks"
	_ "github.com/pingworlds/pong/proxy/ss"
	_ "github.com/pingworlds/pong/proxy/vless"
	_ "github.com/pingworlds/pong/xnet/transport/h1"
	_ "github.com/pingworlds/pong/xnet/transport/h2"
	_ "github.com/pingworlds/pong/xnet/transport/h3"
	_ "github.com/pingworlds/pong/xnet/transport/tcp"
	_ "github.com/pingworlds/pong/xnet/transport/ws"
)

var cfg = config.Config

var jsonErr = "json data error"

type Stream interface {
	event.Stream
}

type Protector interface {
	xnet.SocketProtector
}

func SetConfig(bytes []byte) (s string) {
	err := json.Unmarshal(bytes, cfg)
	if err != nil {
		s = fmt.Errorf("%s, %v", jsonErr, err).Error()
	}
	return s
}

func StartLocal(p Protector) {
	proxy.StartLocal(p)
}

func StopLocal() {
	proxy.StopLocal()
}

func StartRemote() {
	proxy.StartRemote()
}

func StopRemote() {
	proxy.StopRemote()
}

func LocalStat() []byte {
	st := proxy.LocalStat()
	b, _ := json.Marshal(st)
	st = nil
	return b
}

func LocalStatTunnel() []byte {
	st := proxy.LocalStatTunnel()
	b, _ := json.Marshal(&st)
	st = nil
	return b
}

func ClearLocalTunnels() {
	proxy.ClearLocalTunnels()
}

func CloseLocalTunnel(id string) {
	proxy.CloseLocalTunnel(id)
}

func CheckPoints(b []byte, s Stream) {
	event.Listen(s, func(ctx context.Context, notify event.Notify) {
		var points []*xnet.Point
		var err error
		if err = json.Unmarshal(b, &points); err != nil {
			notify(event.NewEvent(event.DATA, nil, err))
			return
		}
		proxy.CheckPoints(points, notify)
	})
}

func CheckDohs(isDirect bool, b []byte, domain string, s Stream) {
	event.Listen(s, func(ctx context.Context, notify event.Notify) {
		var dohs []*xnet.Doh
		var err error
		if err = json.Unmarshal(b, &dohs); err != nil {
			notify(event.NewEvent(event.DATA, nil, err))
			return
		}
		proxy.CheckDohs(isDirect, dohs, domain, notify)
	})
}

func CheckPassMode(t byte, addr string) byte {
	return rule.How(t, addr)
}

func SubLocalServiceEvent(s Stream) string {
	return proxy.SubLocalServiceEvent(s)
}

func CancelLocalServiceEvent(id string) {
	proxy.CancelLocalServiceEvent(id)
}

func SubLocalTunnelEvent(s Stream) string {
	return proxy.SubLocalTunnelEvent(s)
}

func CancelLocalTunnelEvent(id string) {
	proxy.CancelLocalTunnelEvent(id)
}

func SubRemoteServiceEvent(s Stream) string {
	return proxy.SubRemoteServiceEvent(s)
}

func CancelRemoteServiceEvent(id string) {
	proxy.CancelRemoteServiceEvent(id)
}

func SubRemoteTunnelEvent(s Stream) string {
	return proxy.SubRemoteTunnelEvent(s)
}

func CancelRemoteTunnelEvent(id string) {
	proxy.CancelRemoteTunnelEvent(id)
}

func CheckProxyMode(b []byte) byte {
	addr := xnet.Addr(b)
	host, _ := addr.HostAndPort()
	return rule.How(addr[0], host)
}

func DnsLocalQuery(b []byte) []byte {
	return proxy.DnsLocalQuery(b)
}
func DnsRemoteQuery(b []byte) []byte {
	return proxy.DnsRemoteQuery(b)
}

func DnsMustQuery(b []byte) []byte {
	return proxy.DnsMustQuery(b)
}

func ParseQueryDomain(b []byte) string {
	return proxy.ParseQueryDomain(b)
}

func QueryRule(host string) byte {
	return rule.QueryRule(host)
}

func SetRule(host string, mode byte) {
	rule.SetRule(host, mode)
}
