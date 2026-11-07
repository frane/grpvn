package main

import "testing"

func TestExternalActions(t *testing.T) {
	cases := []struct {
		kind, url string
		allowed   bool
	}{
		{"openExternal", "https://chat.example/oauth/authorize", true},
		{"openEmailDraft", "mailto:tim%40example.test?subject=Join+grpvn&body=Hello%0Ahttps%3A%2F%2Fchat.example%2Fjoin", true},
		{"openExternal", "mailto:tim@example.test", false},
		{"openEmailDraft", "file:///private/secret", false},
		{"openEmailDraft", "mailto:tim@example.test?bcc=other@example.test", false},
		{"openEmailDraft", "mailto:tim@example.test?subject=Hello%0ABcc%3Aother", false},
		{"openEmailDraft", "mailto:tim@example.test,other@example.test", false},
	}
	for _, test := range cases {
		if got := allowedExternalURL(test.kind, test.url); got != test.allowed {
			t.Errorf("%s %s: %t", test.kind, test.url, got)
		}
	}
}
