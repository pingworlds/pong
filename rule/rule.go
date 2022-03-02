package rule

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pingworlds/pong/config"
	"github.com/pingworlds/pong/kit"
	"github.com/pingworlds/pong/xnet"
)

const (
	RULE_WHITE  string = "white"
	RULE_BLACK  string = "black"
	RULE_REJECT string = "reject"
	RULE_AUTO   string = "auto"

	PASS_WHITE  string = "white"
	PASS_BLACK  string = "black"
	PASS_PROXY  string = "proxy"
	PASS_DIRECT string = "direct"

	MODE_DIRECT byte = 0x00
	MODE_PROXY  byte = 0x01
	MODE_REJECT byte = 0x02
	MODE_UNKOWN byte = 0x05

	SYNTAX_NORMAL byte = 0x00
	SYNTAX_FULL   byte = 0x01
	SYNTAX_REGEXP byte = 0x02
)

var save_time_out int64 = 5 * 60 * 1000
var net_errs = []string{"timeout", "forcibly closed", "NetWork", "host", "unreachable", "Permission denied", "Desination address"}

var cfg = config.Config

var PreDomainBlackList,
	PreDomainWhiteList,
	PreDomainRejectList,
	PreIpBlackList,
	PreIpWhiteList,
	PreIpRejectList,
	MyList,
	AutoTryList *RuleList

var lists = []*RuleList{
	MyList,
	AutoTryList,
	PreDomainBlackList,
	PreDomainWhiteList,
	PreDomainRejectList,
	PreIpBlackList,
	PreIpWhiteList,
	PreIpRejectList,
}

var inited = false

func Inited() bool {
	return inited
}

func Init(create bool) {
	dir := cfg.WorkDir
	domainInDir := dir + "/domain"
	ipInDir := dir + "/ip"
	outDir := dir + "/rule"

	MyList = NewSiteList(outDir + "/my_rule.txt")
	AutoTryList = NewSiteList(outDir + "/auto_try.txt")

	PreDomainBlackList = NewDomainList(outDir + "/domain-black.txt")
	PreDomainWhiteList = NewDomainList(outDir + "/domain-white.txt")
	PreDomainRejectList = NewDomainList(outDir + "/domain-reject.txt")

	PreIpBlackList = NewIPList(outDir + "/ip-black.txt")
	PreIpWhiteList = NewIPList(outDir + "/ip-white.txt")
	PreIpRejectList = NewIPList(outDir + "/ip-reject.txt")

	if error := os.MkdirAll(outDir, 0777); error != nil {
		log.Println(error)
	}

	MyList.Load()
	AutoTryList.Load()

	loadlist(true, cfg.DomainRules, domainInDir, false)
	loadlist(false, cfg.IPRules, ipInDir, false)
	inited = true
}

func Clear() {
	if !inited {
		return
	}

	for i := range lists {
		lists[i].RemoveAll(nil)
	}
	inited = false
}

func isWhiteOrBlackMode() bool {
	return cfg.DomainMode == PASS_BLACK || cfg.DomainMode == PASS_WHITE || cfg.IPMode == PASS_BLACK || cfg.IPMode == PASS_WHITE
}

func loadlist(isDomain bool, rules []*xnet.Rule, inDir string, create bool) { //(blacklist, whitelist, rejectlist *RuleList) {
	var blacklist, whitelist, rejectlist *RuleList
	if isDomain {
		blacklist = PreDomainBlackList
		whitelist = PreDomainWhiteList
		rejectlist = PreDomainRejectList
	} else {
		blacklist = PreDomainBlackList
		whitelist = PreIpWhiteList
		rejectlist = PreIpRejectList
	}
	for _, rule := range rules {
		path := inDir + "/" + rule.FileName
		if rule.Disabled || !isWhiteOrBlackMode() && rule.Type != RULE_REJECT {
			continue
		}
		var list = blacklist
		if rule.Type == RULE_WHITE {
			list = whitelist
		} else if rule.Type == RULE_REJECT {
			if !cfg.RejectMode {
				continue
			}
			list = rejectlist
		}

		log.Printf("load from path  %s\n", path)
		if err := list.LoadFrom(path); err != nil {
			log.Println(err)
		}
	}

	log.Printf("predefine black list count :%d\n", blacklist.Count())
	log.Printf("predefine white list count :%d\n", whitelist.Count())
	log.Printf("predefine reject list count :%d\n", rejectlist.Count())

	if create {
		blacklist.Save()
		rejectlist.Save()
		whitelist.Save()
	}
}

func How(t byte, host string) (mode byte) {
	mode = MODE_DIRECT
	if !inited {
		return
	}
	if isWhiteOrBlackMode() {
		s := MyList.Get(host)
		if s == nil {
			if AutoTryList.Hit(host) {
				return MODE_PROXY
			}
		} else {
			return s.(*site).Mode
		}
	}

	if t == xnet.DOMAIN {
		if cfg.RejectMode && PreDomainRejectList.Hit(host) {
			return MODE_REJECT
		} else if cfg.DomainMode == PASS_BLACK {
			if PreDomainWhiteList.Hit(host) {
				return
			} else if PreDomainBlackList.Hit(host) {
				return MODE_PROXY
			}
			return
		} else if cfg.DomainMode == PASS_WHITE {
			if PreDomainBlackList.Hit(host) {
				return MODE_PROXY
			} else if PreDomainWhiteList.Hit(host) {
				return
			}
			return MODE_PROXY
		} else if cfg.DomainMode == PASS_PROXY {
			return MODE_PROXY
		}
	} else {
		if cfg.RejectMode && PreIpRejectList.Hit(host) {
			return MODE_REJECT
		} else if cfg.IPMode == PASS_WHITE {
			if PreIpBlackList.Hit(host) {
				return MODE_PROXY
			} else if PreIpWhiteList.Hit(host) {
				return
			}
			return MODE_PROXY
		} else if cfg.IPMode == PASS_BLACK {
			if PreIpWhiteList.Hit(host) {
				return
			} else if PreIpBlackList.Hit(host) {
				return MODE_PROXY
			}
			return
		} else if cfg.IPMode == PASS_PROXY {
			return MODE_PROXY
		}
	}
	return
}

func CanAutoTry(s string) bool {
	for _, e := range net_errs {
		if strings.Contains(s, e) {
			return true
		}
	}
	return false
}

var Timeout int64 = 30 * 60 * 1000

func LazySave() {
	AutoTryList.List.(*sitelist).lazySave()
	MyList.List.(*sitelist).lazySave()
}

func AddToAutoList(addrType byte, host string) {
	AutoTryList.List.(*sitelist).AddOrUpdate(MODE_PROXY, host)
}

func QueryRule(host string) byte {
	s := MyList.Get(host)
	if s == nil {
		return MODE_UNKOWN
	}
	return s.(*site).Mode
}

func SetRule(host string, mode byte) {
	l := MyList.List.(*sitelist)
	if mode == MODE_UNKOWN {
		l.RemoveByHost(host)
	} else {
		AddOrUpdateMyRules(mode, host)
	}
}

func AddOrUpdateMyRules(mode byte, host string) {
	MyList.List.(*sitelist).AddOrUpdate(mode, host)
}

func RemoteFromMyRule(host string) {
	MyList.List.(*sitelist).RemoveByHost(host)
}

type List interface {
	kit.OrderedMap

	Hit(s string) bool

	ParseOne(b []byte) (h hoster, err error)

	SaveOne(w io.Writer, v interface{}) (err error)
}

type RuleList struct {
	List
	Path   string
	Inited bool
}

func (l RuleList) IsEmpty() bool {
	return l.Count() == 0
}

func (l RuleList) Save() (err error) {
	return l.SaveTo(l.Path)
}

func (l RuleList) SaveTo(filePath string) (err error) {
	var file *os.File
	if file, err = os.OpenFile(l.Path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644); err != nil {
		log.Println(err)
		return
	}
	defer file.Close()
	w := bufio.NewWriter(file)

	l.ForEach(func(v interface{}) bool {
		l.SaveOne(w, v)
		return true
	})
	w.Flush()
	return nil
}

func (l RuleList) Load() (err error) {
	return l.LoadFrom(l.Path)
}

func (l RuleList) LoadFrom(filePath string) (err error) {
	var file *os.File
	if file, err = os.Open(filePath); err != nil {
		return
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		b, _, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			break
		}
		var h hoster
		if h, err = l.ParseOne(b); err != nil {
			log.Println(err)
			continue
		}
		l.Put(h.Host(), h)
	}

	return
}

func NewDomainList(path string) *RuleList {
	return &RuleList{Path: path, List: &domainlist{
		Domains: kit.NewOrderedMap(),
		Regs:    kit.NewOrderedMap(),
	}}
}

type domainlist struct {
	Domains kit.OrderedMap
	Regs    kit.OrderedMap
}

func (l domainlist) Put(k, v interface{}) {
	d, ok := v.(*Domain)
	if !ok {
		return
	}
	if d.Syntax == SYNTAX_REGEXP {
		l.Regs.Put(k, d)
		return
	}
	l.Domains.Put(k, d)
}

func (l domainlist) Count() int {
	return l.Domains.Count() + l.Regs.Count()
}

func (l domainlist) SaveOne(w io.Writer, v interface{}) (err error) {
	if t, ok := v.(*Domain); ok {
		s := t.Name
		if t.Syntax == SYNTAX_FULL {
			s = "full:" + s
		} else if t.Syntax == SYNTAX_REGEXP {
			s = "regexp:" + s
		}
		_, err = fmt.Fprintf(w, "%s\n", s)
	}
	return
}

func (l domainlist) ParseOne(b []byte) (h hoster, err error) {
	return NewDomain(b)
}

func (l domainlist) GetDomain(host string) (d *Domain) {
	t := l.Domains.Get(host)
	if t == nil {
		t = l.Regs.Get(host)
	}
	if d, ok := t.(*Domain); ok {
		return d
	}
	return nil
}

func (l domainlist) Get(k interface{}) interface{} {
	t := l.Domains.Get(k)
	if t == nil {
		t = l.Regs.Get(k)
	}
	return t
}

func (l domainlist) Index(i int) interface{} {
	return nil
}

func (l domainlist) getDomain(host string) (d *Domain) {
	t := l.Domains.Get(host)
	if d, ok := t.(*Domain); ok {
		return d
	}
	return nil
}

func (l domainlist) Hit(host string) bool {
	return l.Query(host) != nil
}

func (l domainlist) Query(host string) (d *Domain) {
	if len(host) < 2 {
		return
	}
	if len(host) > 6 && host[:4] == "www." {
		host = host[4:]
	}
	d = l.QueryByDomain(host)
	if d == nil {
		d = l.QueryByReg(host)
	}
	return d
}

func (l domainlist) QueryByDomain(host string) (d *Domain) {
	if len(host) < 2 {
		return
	}
	arr := strings.Split(host, ".")
	for i := len(arr) - 1; i >= 0; i-- {
		s := strings.Join(arr[i:], ".")
		d = l.getDomain(s)
		if d != nil {
			if d.Syntax == SYNTAX_FULL && i != 0 {
				continue
			}
			return
		}
	}
	return
}

func (l domainlist) QueryByReg(host string) (d *Domain) {
	if len(host) < 2 {
		return
	}
	var dt *Domain
	var t interface{}
	for i := 0; i < l.Regs.Count(); i++ {
		if t = l.Regs.Index(i); t == nil {
			continue
		}

		dt = t.(*Domain)
		if rs, _ := regexp.MatchString(dt.Name, host); rs {
			d = dt
			break
		}
	}
	return
}

func (l domainlist) Remove(k interface{}) {
	l.Regs.Remove(k)
	l.Domains.Remove(k)
}

func (l domainlist) RemoveAll(fn kit.Filter) {
	l.Regs.RemoveAll(fn)
	l.Domains.RemoveAll(fn)
}

func (l domainlist) ForEach(fn kit.Filter) {
	l.Domains.ForEach(fn)
	l.Regs.ForEach(fn)
}

func NewIPList(path string) *RuleList {
	return &RuleList{Path: path, List: &iplist{OrderedMap: kit.NewOrderedMap()}}
}

type iplist struct {
	kit.OrderedMap
}

func (l iplist) SaveOne(w io.Writer, v interface{}) (err error) {
	_, err = fmt.Fprintf(w, "%s\n", v.(*IP).Host())
	return
}

func (l iplist) ParseOne(b []byte) (hoster, error) {
	return NewIP(string(b))
}

func (l iplist) Hit(ipstr string) bool {
	ip := net.ParseIP(ipstr)

	// log.Printf("ip str %s    after parse %v\n", ipstr, ip)
	if ip == nil || ip.IsPrivate() {
		return false
	}

	for i := 0; i < l.Count(); i++ {
		if t := l.Index(i); t != nil {
			if t.(*IP).Contains(ip) {
				return true
			}
		}
	}
	return false
}

func NewDomain(b []byte) (d *Domain, err error) {
	syntax := SYNTAX_NORMAL
	if len(b) > 8 && string(b[:7]) == "regexp:" {
		b = b[7:]
		syntax = SYNTAX_REGEXP
	} else if len(b) > 6 && string(b[:5]) == "full:" {
		//as:full:adservice.google.com
		b = b[5:]
		syntax = SYNTAX_FULL
	} else if len(b) > 5 && string(b[:4]) == "www." {
		b = b[4:]
	}

	s := string(b)

	if syntax != SYNTAX_REGEXP && !strings.Contains(s, ".") {
		s = fmt.Sprintf("%s.com", s)
	}

	d = &Domain{Name: s, Syntax: syntax}
	return
}

func NewIP(ipstr string) (ip *IP, err error) {
	var ipnet *net.IPNet
	if _, ipnet, err = net.ParseCIDR(ipstr); err != nil {
		return
	}
	t := IP{IPNet: ipnet}
	return &t, nil
}

type hoster interface {
	Host() string
}

type Domain struct {
	Name   string
	Syntax byte
}

func (d Domain) Host() string {
	return d.Name
}

type IP struct {
	*net.IPNet
}

func (i IP) Host() string {
	return i.String()
}

func FileNotExist(path string) bool {
	_, err := os.Stat(path)
	return err != nil && errors.Is(err, os.ErrNotExist)
}

func NewSite(mode byte, host string) *site {
	return &site{Mode: mode, Address: host}
}

func NewSiteFromBytes(b []byte) (s *site, err error) {
	str := string(b)
	arr := strings.Split(str, " ")
	var m int
	if m, err = strconv.Atoi(arr[1]); err != nil {
		return
	}
	s = &site{Address: arr[0], Mode: byte(m)}
	return
}

type site struct {
	Mode    byte
	Address string
	Count   int
}

func (s site) Host() string {
	return s.Address
}

func (s site) String() string {
	return fmt.Sprintf("%s %d", s.Address, s.Mode)
}

func NewSiteList(path string) *RuleList {
	l := &RuleList{Path: path, List: &sitelist{
		OrderedMap: kit.NewOrderedMap(),
	}}
	l.List.(*sitelist).r = l
	return l
}

type sitelist struct {
	kit.OrderedMap
	lastSaveTime int64
	UnSaveCount  int
	r            *RuleList
}

func (l sitelist) SaveOne(w io.Writer, v interface{}) (err error) {
	_, err = fmt.Fprintf(w, "%s\n", v.(*site).String())
	return
}

func (l sitelist) ParseOne(b []byte) (hoster, error) {
	return NewSiteFromBytes(b)
}

func (l sitelist) Hit(host string) bool {
	return l.Get(host) != nil
}

func (l sitelist) How(host string) (m byte) {
	if s := l.Get(host); s != nil {
		return s.(*site).Mode
	}
	return MODE_UNKOWN
}

func (l sitelist) AddOrUpdate(mode byte, host string) {
	s := l.Get(host)
	if s == nil {
		l.Put(host, NewSite(mode, host))
	} else {
		s.(*site).Mode = mode
	}
	l.UnSaveCount++
	l.lazySave()
}

func (l *sitelist) lazySave() {
	if l.UnSaveCount > 10 || l.UnSaveCount > 0 && (time.Now().UnixMilli()-l.lastSaveTime) > save_time_out {
		l.r.Save()
		l.lastSaveTime = time.Now().UnixMilli()
	}
}

func (l sitelist) RemoveByHost(host string) {
	l.OrderedMap.Remove(host)
	l.UnSaveCount++
	l.lazySave()
}
