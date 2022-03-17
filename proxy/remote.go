package proxy

import (
	"context"
	"fmt"

	"github.com/pingworlds/pong/event"
	"github.com/pingworlds/pong/xnet"
)

var rmts *container

func init() {
	ctx := context.Background()
	sp := event.NewProvider(100, ctx)
	cp := event.NewProvider(100, ctx)
	rmts = newContainer(&remoteCtrl{ctrl: newCtrl(sp, cp, false)})
	cp.Start()
	sp.Start()
}

func NewRemoteProto(point *xnet.Point) (*Proto, error) {
	return rmts.NewProto(point)
}

func SubRemoteServiceEvent(s event.Stream) string {
	return rmts.SubServiceEvent(s)
}

func CancelRemoteServiceEvent(id string) {
	rmts.CancelServiceEvent(id)
}

func SubRemoteTunnelEvent(s event.Stream) string {
	return rmts.SubTunnelEvent(s)
}

func CancelRemoteTunnelEvent(id string) {
	rmts.CancelTunnelEvent(id)
}

func StartRemote() {
	rmts.Start()
}

func StopRemote() {
	rmts.ClearTunnels()
	rmts.ClearPeers()
	rmts.Stop()
}

type remoteCtrl struct {
	*ctrl
}

func (r *remoteCtrl) NewProto(point *xnet.Point) (proto *Proto, err error) {
	if proto, err = r.ctrl.newProto(point); err != nil {
		return
	}
	proto.ctrl = r
	return
}

func (r *remoteCtrl) NewPeer(point *xnet.Point) (p Peer, err error) {
	var ptc *Protocol
	if ptc, err = GetProtocol(point.Protocol); err != nil {
		return
	}
	var proto *Proto
	if proto, err = r.NewProto(point); err != nil {
		return
	}
	p = ptc.RemoteFn(proto)
	proto.Do = r.readyDo(p)
	return
}

func (r *remoteCtrl) NewListenPeer(point *xnet.Point) (p Peer, err error) {
	if p, err = r.NewPeer(point); err == nil {
		r.PutPeer(point.ID(), p)
	}
	return
}

func (r *remoteCtrl) readyDo(f Filter) Do {
	return func(t *Tunnel) (err error) {
		defer t.CloseWithError(err)

		if t.Method == CONNECT {
			t.Dst, err = xnet.Dial(t.Addr)
		} else {
			err = fmt.Errorf("method %x not supported", t.Method)
			f.BeforeDial(t, err)
			return
		}
		if err = f.AfterDial(t, err); err != nil {
			return err
		}

		return r.relay(f, t)
	}
}
