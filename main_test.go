package main

import "testing"

func TestParseProfileNBSP(t *testing.T) {
	html := "<li class=\"ratio-bar__uploaded\"><a href=\"x\">\n<i class=\"fas\"></i>\n 50 GiB\n</a></li>" +
		"<li class=\"ratio-bar__downloaded\"><a href=\"y\">\n<i class=\"fas\"></i>\n 1 GiB\n</a></li>"
	m, err := parseProfile(html)
	if err != nil {
		t.Fatal(err)
	}
	if m.Uploaded != 50*1024*1024*1024 || m.Downloaded != 1024*1024*1024 {
		t.Fatalf("got %+v", m)
	}
}
