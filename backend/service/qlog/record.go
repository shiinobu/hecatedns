package qlog

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shiinobu/hecatedns/backend/engine"
)

const (
	filePrefix = "queries-"
	fileSuffix = ".jsonl"
	dayLayout  = "20060102"
	tsLayout   = "2006-01-02T15:04:05.000Z07:00"

	maxFieldLen = 255
	maxErrorLen = 512

	replacement = '?'
)

type Record struct {
	TS       string `json:"ts"`
	Client   string `json:"client,omitempty"`
	Proto    string `json:"proto,omitempty"`
	QName    string `json:"qname"`
	QType    string `json:"qtype,omitempty"`
	Action   string `json:"action"`
	Reason   string `json:"reason,omitempty"`
	Source   string `json:"source,omitempty"`
	Via      string `json:"via,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	RCode    string `json:"rcode,omitempty"`
	Error    string `json:"error,omitempty"`
	MS       int64  `json:"ms"`
	Cached   *bool  `json:"cached,omitempty"`

	Time time.Time `json:"-"`
}

func newRecord(e engine.Entry, loc *time.Location, fallback time.Time) Record {
	at := e.Time
	if at.IsZero() {
		at = fallback
	}
	at = at.In(loc)
	rec := Record{
		TS:       at.Format(tsLayout),
		Proto:    sanitize(e.Proto, maxFieldLen),
		QName:    sanitize(e.QName, maxFieldLen),
		QType:    sanitize(e.QType, maxFieldLen),
		Action:   sanitize(e.Action, maxFieldLen),
		Reason:   sanitize(e.Reason, maxFieldLen),
		Source:   sanitize(e.Source, maxFieldLen),
		Via:      sanitize(e.Via, maxFieldLen),
		Upstream: sanitize(e.Upstream, maxFieldLen),
		RCode:    sanitize(e.RCode, maxFieldLen),
		Error:    sanitize(e.Error, maxErrorLen),
		MS:       e.Latency.Milliseconds(),
		Time:     at,
	}
	if e.Client.IsValid() {
		rec.Client = e.Client.String()
	}
	if e.Action == engine.ActionPass {
		cached := e.Cached
		rec.Cached = &cached
	}
	return rec
}

func sanitize(s string, limit int) string {
	var b strings.Builder
	b.Grow(min(len(s), limit))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			r = replacement
		} else if !unicode.IsPrint(r) {
			r = replacement
		}
		if b.Len()+utf8.RuneLen(r) > limit {
			break
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}
