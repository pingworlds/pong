package xnet

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var Err_NotImplement = errors.New("not implement")
var Err_NoUsefulPoint = errors.New("no useful point")
var Err_NoCertFile = errors.New("no cert or key file")
var Err_NotBinaryMessage = errors.New("not binary message")

type IDCode interface {
	ID() string
	Code() string
}

func NewUUID() UUID {
	return UUID(uuid.New())
}

func NewString() string {
	return uuid.NewString()
}

type UUID uuid.UUID

func (i UUID) ID() string {
	return uuid.UUID(i).String()
}

func (i UUID) Code() string {
	return hex.EncodeToString(i[:2])
}

type Point struct {
	Id           string
	Name         string
	Protocol     string //vless,socks,ss
	Transport    string //http,h2,h3,ws
	Host         string
	Path         string
	Clients      []string
	CertFile     string
	KeyFile      string
	Sni          string
	InsecureSkip bool
	Disabled     bool
	SupportRelay bool
	Latency      int
	Breaking     bool
	BreakTime    int64
}

func (p *Point) ID() string {
	if p.Id == "" {
		p.Id = uuid.NewString()
	}
	return p.Id
}

func (p *Point) Code() string {
	code := p.ID()
	if len(code) > 4 {
		code = code[:4]
	}
	return code
}

func (p Point) String() string {
	s := fmt.Sprintf("\nname:%s  Id:%s\n", p.Name, p.Id)
	s = s + fmt.Sprintf("Host:%s  Path:%s\n", p.Host, p.Path)
	s = s + fmt.Sprintf("Transport:%s  Protocol:%s\n", p.Transport, p.Protocol)
	s = s + fmt.Sprintf("Clients:%s \n", p.Clients)
	s = s + fmt.Sprintf("Cert:%s Key:%s\n", p.CertFile, p.KeyFile)
	s = s + fmt.Sprintf("Disabled:%v \n", p.Disabled)
	return s
}

type Rule struct {
	Name string

	Type string

	FileName string

	Url string

	Disabled bool

	LastTime int64
}

func (r Rule) String() string {
	s := fmt.Sprintf("\nname:%s  Type:%x\n", r.Name, r.Type)
	s = s + fmt.Sprintf("FileName:%s   \n", r.FileName)
	s = s + fmt.Sprintf("URL:%s  \n", r.Url)
	s = s + fmt.Sprintf("Disabled:%v \n", r.Disabled)

	return s
}
