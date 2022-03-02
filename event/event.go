package event

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/pingworlds/pong/xnet"

	"github.com/google/uuid"
)

const (
	DATA  byte = 0
	START byte = 1
	END   byte = 2
)

type Event struct {
	Type  byte
	Error string
	Data  interface{}
}

func NewEvent(t byte, data interface{}, err error) Event {
	ev := Event{Type: t, Data: data}
	if err != nil {
		ev.Error = err.Error()
	}
	// b, _ := json.Marshal(ev)
	// log.Println(string(b))
	return ev
}

func NewStartEvent() Event {
	return Event{Type: START}
}

func NewEndEvent() Event {
	return Event{Type: END}
}

func NewProvider(capacity int, ctx context.Context) *provider {
	return NewProviderWithStream(capacity, nil, ctx)
}

func NewProviderWithStream(capacity int, s Stream, ctx context.Context) *provider {
	if ctx == nil {
		ctx = context.Background()
	}
	p := &provider{
		UUID:     xnet.NewUUID(),
		subs:     map[string]Stream{},
		capacity: capacity,
		pctx:     ctx,
		sleeping: false,
	}
	if s != nil {
		p.Sub(s)
	}
	return p
}

type Stream interface {
	OnData(b []byte)
}

type stream struct{}

func (s *stream) OnData(b []byte) {
	log.Printf("stream data:%s\n", string(b))
}

func NewStream() *stream {
	return &stream{}
}

type Notify func(data interface{})

func (fn Notify) Notify(data interface{}) {
	fn(data)
}

type Notifier interface {
	Notify(data interface{})
}

type Provider interface {
	Notifier
	xnet.IDCode
	Start()
	Close()
	Sub(s Stream) (id string)
	Cancel(id string)
	CancelAll()
}

type provider struct {
	xnet.UUID
	pctx     context.Context
	ctx      context.Context
	cancel   context.CancelFunc
	ch       chan interface{}
	subs     map[string]Stream
	mu       sync.Mutex
	capacity int
	inited   bool
	sleeping bool
}

func (p *provider) Start() {
	p.ready()
	p.Wakeup()
}

func (p *provider) ready() {
	if p.inited {
		return
	}
	p.ch = make(chan interface{}, p.capacity)
	p.Wakeup()
	p.inited = true
}

func (p *provider) Wakeup() {
	p.sleeping = false
	go func() {
		p.ctx, p.cancel = context.WithCancel(p.pctx)
		for {
			select {
			case <-p.ctx.Done():
				return
			case msg := <-p.ch:
				p.pub(msg)
			}
		}
	}()
}

func (p *provider) Sleep() {
	if !p.inited || p.sleeping {
		return
	}
	p.sleeping = true
	ch := make(chan struct{})
	go func() {
		for {
			<-time.After(10 * time.Millisecond)
			if len(p.ch) == 0 {
				ch <- struct{}{}
				return
			}
		}
	}()
	<-ch
	p.cancel()
}

func (p *provider) Close() {
	if !p.inited {
		return
	}
	p.Sleep()
	close(p.ch)
	p.CancelAll()
	p.inited = false
}

func (p *provider) Sub(s Stream) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := uuid.NewString()
	p.subs[id] = s
	return id
}

func (p *provider) Cancel(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.subs, id)
}

func (p *provider) CancelAll() {
	for id := range p.subs {
		delete(p.subs, id)
	}
}

func (p *provider) Notify(data interface{}) {
	if p.sleeping || len(p.subs) == 0 || data == nil {
		return
	}
	p.ch <- data
}

func (p *provider) pub(m interface{}) {
	if len(p.subs) == 0 || m == nil {
		return
	}

	b, err := json.Marshal(m)
	if err != nil {
		log.Println(err)
		return
	}
	for _, sub := range p.subs {
		sub.OnData(b)
	}
}

type Job func(ctx context.Context, notify Notify)

func Listen(s Stream, job Job) {
	ListenWithContext(s, nil, job)
}

func ListenWithContext(s Stream, ctx context.Context, job Job) {
	p := NewProviderWithStream(10, s, ctx)
	p.Start()
	p.Notify(NewStartEvent())
	job(ctx, p.Notify)
	p.Notify(NewEndEvent())
	p.Close()
}
