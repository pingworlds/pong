package config

import (
	"fmt"
	"time"

	"github.com/pingworlds/pong/xnet"
)

type config struct {
	Timeout        int
	StatTime       time.Duration
	PeekTime       time.Duration
	WorkDir        string
	LocalDohMode   bool
	RemoteDohMode  bool
	WorkDohs       []*xnet.Doh
	Listens        []*xnet.Point
	Points         []*xnet.Point
	IPRules        []*xnet.Rule
	DomainRules    []*xnet.Rule
	RejectMode     bool
	DomainMode     string
	IPMode         string
	AutoTry        bool
	AutoOrderPoint bool
	PerMaxCount    int
}

func (c config) String() string {
	s := fmt.Sprintf("\n  listener number:%d  ", len(c.Listens))
	s += fmt.Sprintf("\n     %s", c.Listens)
	s += fmt.Sprintf("\n  point number:%d  ", len(c.Points))
	s += fmt.Sprintf("\n     %s", c.Points)
	s += fmt.Sprintf("\n  domain rule number:%d  ", len(c.DomainRules))
	s += fmt.Sprintf("\n     %s", c.DomainRules)
	s += fmt.Sprintf("\n  IP rule number:%d  ", len(c.IPRules))
	s += fmt.Sprintf("\n     %s", c.IPRules)
	s += fmt.Sprintf("\n  lcoal doh mode:%v,  remote doh mode:%v", c.LocalDohMode, c.RemoteDohMode)

	return s
}

var Config = &config{}
