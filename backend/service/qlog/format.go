package qlog

import (
	"fmt"
	"strings"
	"time"
)

const (
	tagBlock = "BLOK "
	tagPass  = "LOLOS"
	tagError = "GALAT"

	displayTimeLayout = "2006-01-02 15:04:05.000"
	noClient          = "-"
)

func (r Record) Line(loc *time.Location) string {
	client := r.Client
	if client == "" {
		client = noClient
	}
	parts := []string{r.Time.In(loc).Format(displayTimeLayout), actionTag(r.Action), client, r.QType, r.QName}
	if r.Action == "block" {
		parts = append(parts, blockDetail(r)...)
	} else {
		parts = append(parts, answerDetail(r)...)
	}
	return strings.Join(parts, " ")
}

func actionTag(action string) string {
	switch action {
	case "block":
		return tagBlock
	case "pass":
		return tagPass
	case "error":
		return tagError
	}
	return fmt.Sprintf("%-5s", strings.ToUpper(action))
}

func blockDetail(r Record) []string {
	var parts []string
	if why := reasonSource(r); why != "" {
		parts = append(parts, why)
	}
	if r.Via != "" {
		parts = append(parts, "via", r.Via)
	}
	return parts
}

func answerDetail(r Record) []string {
	var parts []string
	if r.RCode != "" {
		parts = append(parts, r.RCode)
	}
	parts = append(parts, fmt.Sprintf("%dms", r.MS))
	switch {
	case r.Cached != nil && *r.Cached:
		parts = append(parts, "cache")
	case r.Upstream != "":
		parts = append(parts, r.Upstream)
	}
	if why := reasonSource(r); why != "" {
		parts = append(parts, why)
	}
	if r.Error != "" {
		parts = append(parts, r.Error)
	}
	return parts
}

func reasonSource(r Record) string {
	switch {
	case r.Reason != "" && r.Source != "":
		return r.Reason + ":" + r.Source
	case r.Reason != "":
		return r.Reason
	}
	return r.Source
}
