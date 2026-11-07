package main

import (
	"net/mail"
	"net/url"
	"strings"
)

func allowedExternalURL(kind, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return false
	}
	if kind == "openExternal" {
		return u.Scheme == "https" || u.Scheme == "http"
	}
	if kind != "openEmailDraft" || u.Scheme != "mailto" || u.Host != "" || len(raw) > 8192 {
		return false
	}
	to, err := url.PathUnescape(u.Opaque)
	if err != nil || strings.ContainsAny(to, "\r\n") {
		return false
	}
	address, err := mail.ParseAddress(to)
	if err != nil || address.Address != to {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for key := range query {
		if key != "subject" && key != "body" {
			return false
		}
	}
	return !strings.ContainsAny(query.Get("subject"), "\r\n")
}
