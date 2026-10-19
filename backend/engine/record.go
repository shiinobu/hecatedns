package engine

import (
	"net/netip"
	"time"
)

const (
	ActionPass  = "pass"
	ActionBlock = "block"
	ActionError = "error"
)

const (
	ReasonRule    = "rule"
	ReasonList    = "list"
	ReasonCNAME   = "cname"
	ReasonSpecial = "special"
	ReasonACL     = "acl"
	ReasonAllow   = "allow"
	ReasonInvalid = "invalid"
)

type Entry struct {
	Time     time.Time
	Client   netip.Addr
	Proto    string
	QName    string
	QType    string
	Action   string
	Reason   string
	Source   string
	Via      string
	Upstream string
	RCode    string
	Error    string
	Latency  time.Duration
	Cached   bool
}

type Recorder interface {
	Record(Entry)
}
