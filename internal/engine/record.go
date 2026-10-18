package engine

import (
	"net/netip"
	"time"
)

// Values of Entry.Action.
const (
	ActionPass  = "pass"
	ActionBlock = "block"
	ActionError = "error"
)

// Values of Decision.Reason and Entry.Reason.
const (
	ReasonRule    = "rule"
	ReasonList    = "list"
	ReasonCNAME   = "cname"
	ReasonSpecial = "special"
	ReasonACL     = "acl"
	ReasonAllow   = "allow"
)

// Entry is one query-log record. The engine writes exactly one per query. The
// strings may contain attacker-controlled text (names, errors); the Recorder
// is responsible for sanitizing them before they reach a file.
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

// Recorder receives the query-log records. Record must be safe for concurrent
// use and must never block the caller: when it cannot keep up it drops the
// record (the qlog package counts the drops).
type Recorder interface {
	Record(Entry)
}
