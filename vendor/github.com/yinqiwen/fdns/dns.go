package fdns

import (
	"errors"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	UseFastDNS    = 0
	UseTrustedDNS = 1

	Poisioned    = 1
	NotPoisioned = 0
	Unknown      = -1
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

var letterRunes = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randAsciiString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

var ErrDNSEmpty = errors.New("No DNS record found")
var ErrDNSTimeout = errors.New("DNS timeout")

type ServerConfig struct {
	Server      string
	Timeout     int
	MaxResponse int

	network string
	addr    string
	timeout time.Duration
}

func (c *ServerConfig) init() {
	if c.MaxResponse == 0 {
		c.MaxResponse = 1
	}
	if !strings.Contains(c.Server, "://") {
		c.network = "udp"
		c.addr = c.Server
	} else {
		u, _ := url.Parse(c.Server)
		c.network = u.Scheme
		c.addr = u.Host
	}
	if !strings.Contains(c.addr, ":") {
		c.addr = c.addr + ":53"
	}
	if c.Timeout == 0 {
		c.Timeout = 800
	}
	c.timeout = time.Duration(c.Timeout) * time.Millisecond
}

type Config struct {
	Listen     string
	FastDNS    []ServerConfig
	TrustedDNS []ServerConfig
	MinTTL     uint32
	//0:no 1:yes -1:unknown
	IsDomainPoisioned func(string) int
	DialTimeout       func(network, addr string, timeout time.Duration) (net.Conn, error)
	IsCNIP            func(ip net.IP) bool
}

type TrustedDNS struct {
	DomainMarkSet sync.Map
	Config        Config
}

func selectIP(ips []net.IP) net.IP {
	var ip net.IP
	ipLen := len(ips)
	if ipLen == 1 {
		ip = ips[0]
	} else {
		ip = ips[rand.Intn(ipLen)]
	}
	return ip
}
func selectDNSServer(ss []ServerConfig) *ServerConfig {
	var server *ServerConfig
	slen := len(ss)
	if slen == 1 {
		server = &ss[0]
	} else {
		server = &ss[rand.Intn(slen)]
	}
	return server
}

func (t *TrustedDNS) lookup(domain string, trusted bool, rtype uint16) ([]dns.RR, bool, error) {
	var server *ServerConfig
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), rtype)
	waitCount := 1
	polluted := false
	if trusted {
		server = selectDNSServer(t.Config.TrustedDNS)
		m.Compress = true
		o := new(dns.OPT)
		o.Hdr.Name = "."
		o.Hdr.Rrtype = dns.TypeOPT
		e := new(dns.EDNS0_NSID)
		e.Code = dns.EDNS0NSID
		e.Nsid = "AA"
		o.Option = append(o.Option, e)
		m.Extra = append(m.Extra, o)
		//m.SetEdns0(128, false)
		waitCount = server.MaxResponse
	} else {
		server = selectDNSServer(t.Config.FastDNS)
	}
	timeout := time.Now().Add(server.timeout)
	dnsConn := new(dns.Conn)
	var c net.Conn
	var err error
	if nil != t.Config.DialTimeout {
		c, err = t.Config.DialTimeout(server.network, server.addr, server.timeout)
	} else {
		c, err = net.DialTimeout(server.network, server.addr, server.timeout)
	}
	if nil != err {
		return nil, polluted, err
	}
	dnsConn.Conn = c
	dnsConn.WriteMsg(m)
	dnsConn.SetReadDeadline(timeout)
	defer dnsConn.Close()
	var rrs []dns.RR
	for i := 0; i < waitCount; i++ {
		res, err := dnsConn.ReadMsg()
		//log.Printf("###%s %d %v", server.addr, i, res)
		//log.Printf("###%s %v %d", server.addr, err, i)
		if nil == err {
			if trusted && nil == res.IsEdns0() {
				continue
			}
			rrs = res.Answer
			if i > 0 {
				polluted = true
			}
			return rrs, polluted, nil
		}
		break
	}
	if len(rrs) == 0 {
		err = ErrDNSEmpty
	}
	return rrs, polluted, err
}

func (t *TrustedDNS) lookupRecord(domain string, rtype uint16) (ips []dns.RR, err error) {
	isPoisioned := Unknown
	if strings.HasSuffix(domain, ".cn") {
		isPoisioned = NotPoisioned
	}
	if nil != t.Config.IsDomainPoisioned {
		isPoisioned = t.Config.IsDomainPoisioned(domain)
	}
	dnsType := Unknown
	if isPoisioned == Unknown {
		if v, exist := t.DomainMarkSet.Load(domain); exist {
			dnsType = v.(int)
		}
	} else if isPoisioned == Poisioned {
		dnsType = UseTrustedDNS
	} else {
		dnsType = UseFastDNS
	}

	switch dnsType {
	case UseTrustedDNS:
		ips, _, err = t.lookup(domain, true, rtype)
	case UseFastDNS:
		ips, _, err = t.lookup(domain, false, rtype)
	case Unknown:
		var fastResult, trustedResult []dns.RR
		var fastErr, trustedErr error
		polluted := false
		waitCh := make(chan int, 1)
		go func() {
			fastResult, _, fastErr = t.lookup(domain, false, rtype)
			waitCh <- 1
		}()
		trustedResult, polluted, trustedErr = t.lookup(domain, true, rtype)
		if polluted {
			dnsType = UseTrustedDNS
		} else {
			<-waitCh
			if len(fastResult) == 0 && len(trustedResult) > 0 {
				dnsType = UseTrustedDNS
			} else {
				for _, r := range fastResult {
					if a, ok := r.(*dns.A); ok {
						if t.Config.IsCNIP(a.A) {
							dnsType = UseFastDNS
						} else {
							dnsType = UseTrustedDNS
						}
						break
					}
				}
			}
		}
		if dnsType == UseTrustedDNS {
			t.DomainMarkSet.Store(domain, UseTrustedDNS)
			ips, err = trustedResult, trustedErr
		} else {
			t.DomainMarkSet.Store(domain, UseFastDNS)
			ips, err = fastResult, fastErr
		}
	}
	if t.Config.MinTTL > 0 {
		for _, rec := range ips {
			if rec.Header().Ttl < t.Config.MinTTL {
				rec.Header().Ttl = t.Config.MinTTL
			}
		}
	}
	return
}

func (t *TrustedDNS) LookupA(domain string) ([]dns.RR, error) {
	return t.lookupRecord(domain, dns.TypeA)
}
func (t *TrustedDNS) LookupAAAA(domain string) ([]dns.RR, error) {
	return t.lookupRecord(domain, dns.TypeAAAA)
}

func (t *TrustedDNS) Query(r *dns.Msg) (*dns.Msg, error) {
	res := &dns.Msg{}
	res.SetReply(r)
	for _, question := range r.Question {
		domain := question.Name
		domain = domain[0 : len(domain)-1]
		if strings.Contains(domain, ".") {
			rrs, err := t.lookupRecord(domain, question.Qtype)
			if nil == err {
				res.Answer = append(res.Answer, rrs...)
			}
		}
	}
	return res, nil
}

func (t *TrustedDNS) QueryRaw(p []byte) ([]byte, error) {
	req := &dns.Msg{}
	err := req.Unpack(p)
	if nil != err {
		return nil, err
	}
	res, err := t.Query(req)
	if nil != err {
		return nil, err
	}
	data, err := res.Pack()
	return data, err
}

func (t *TrustedDNS) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	res, err := t.Query(r)
	if nil != err {
		res = &dns.Msg{}
		res.SetReply(r)
	}
	w.WriteMsg(res)
}

func (t *TrustedDNS) Start() error {
	return dns.ListenAndServe(t.Config.Listen, "udp", t)
}

func NewTrustedDNS(conf *Config) (*TrustedDNS, error) {
	s := &TrustedDNS{}
	s.Config = *conf

	if len(s.Config.FastDNS) == 0 {
		server := []string{"223.5.5.5", "180.76.76.76"}
		for _, v := range server {
			ss := ServerConfig{
				Server:      v,
				Timeout:     500,
				MaxResponse: 1,
			}
			s.Config.FastDNS = append(s.Config.FastDNS, ss)
		}
	}

	if len(s.Config.TrustedDNS) == 0 {
		server := []string{"208.67.222.222:53", "208.67.220.220:53"}
		for _, v := range server {
			ss := ServerConfig{
				Server:      v,
				Timeout:     800,
				MaxResponse: 5,
			}
			s.Config.TrustedDNS = append(s.Config.TrustedDNS, ss)
		}
	}
	for i := range s.Config.FastDNS {
		s.Config.FastDNS[i].init()
	}
	for i := range s.Config.TrustedDNS {
		s.Config.TrustedDNS[i].init()
	}
	//log.Printf("%v", s.Config)
	return s, nil
}
