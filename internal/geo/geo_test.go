package geo

import (
	"net"
	"testing"
)

func TestFromName(t *testing.T) {
	cases := map[string]string{
		"🇩🇪 Frankfurt-01": "de", "آلمان ریلیتی": "de", "سرور آلمانی": "de", "هلند XHTTP": "nl", "NL-Amsterdam": "nl",
		"US | Fast": "us", "Turkey vmess": "tr", "سرور کوچک": "", "Romania 2": "ro", "Oman": "om", "United Kingdom": "gb",
		"vless-ws-tls": "", "Warp": "", "هنگ کنگ": "hk", "@channel_DE_42": "de", "TLS WS": "",
	}
	for n, want := range cases {
		if got := FromName(n); got != want {
			t.Errorf("%q: got %q want %q", n, got, want)
		}
	}
}

func TestLookup(t *testing.T) {
	cases := map[string]string{"8.8.8.8": "us", "1.1.1.1": "", "185.143.232.1": "ir", "91.107.128.1": "de", "5.75.128.1": "de"}
	for ip, want := range cases {
		got := LookupIP(net.ParseIP(ip))
		if want != "" && got != want {
			t.Errorf("%s: %q want %q", ip, got, want)
		}
		t.Logf("%s → %s", ip, got)
	}
}
