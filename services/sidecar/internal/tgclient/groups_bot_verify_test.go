package tgclient

import "testing"

func TestParseBotStartDeepLink(t *testing.T) {
	cases := []struct {
		raw       string
		username  string
		start     string
		ok        bool
	}{
		{
			raw:      "https://telegram.me/nmnmfunbot?start=joinverify_-1001356915330",
			username: "nmnmfunbot",
			start:    "joinverify_-1001356915330",
			ok:       true,
		},
		{
			raw:      "https://t.me/nmnmfunbot?start=joinverify_-1001356915330",
			username: "nmnmfunbot",
			start:    "joinverify_-1001356915330",
			ok:       true,
		},
		{
			raw:      "t.me/FooBot?start=abc",
			username: "FooBot",
			start:    "abc",
			ok:       true,
		},
		{
			raw:      "tg://resolve?domain=nmnmfunbot&start=joinverify_1",
			username: "nmnmfunbot",
			start:    "joinverify_1",
			ok:       true,
		},
		{
			raw: "https://t.me/nmnmfunbot",
			ok:  false,
		},
		{
			raw: "https://t.me/+AbCdEf",
			ok:  false,
		},
		{
			raw: "https://example.com/bot?start=x",
			ok:  false,
		},
		{
			raw: "/start",
			ok:  false,
		},
		// Mini App 路径不应被当成 bot?start=
		{
			raw: "https://telegram.me/nmnmfunbot/panel?startapp=abc",
			ok:  false,
		},
	}
	for _, c := range cases {
		u, s, ok := ParseBotStartDeepLink(c.raw)
		if ok != c.ok {
			t.Fatalf("%q ok=%v want %v", c.raw, ok, c.ok)
		}
		if !c.ok {
			continue
		}
		if u != c.username || s != c.start {
			t.Fatalf("%q => (%s,%s) want (%s,%s)", c.raw, u, s, c.username, c.start)
		}
	}
}

func TestParsePublicJoinLink(t *testing.T) {
	cases := []struct {
		raw    string
		target string
		ok     bool
	}{
		{raw: "https://t.me/pionexcn", target: "@pionexcn", ok: true},
		{raw: "t.me/pionexcn", target: "@pionexcn", ok: true},
		{raw: "@pionexcn", target: "@pionexcn", ok: true},
		{raw: "https://t.me/+AbCdEfGhIjKlMn", target: "https://t.me/+AbCdEfGhIjKlMn", ok: true},
		{raw: "tg://resolve?domain=pionexcn", target: "@pionexcn", ok: true},
		{raw: "https://t.me/nmnmfunbot?start=joinverify_1", ok: false},
		{raw: "https://t.me/nmnmfunbot/panel?startapp=abc", ok: false},
		{raw: "https://example.com/pionexcn", ok: false},
		{raw: "https://t.me/c/1234567890/1", ok: false},
	}
	for _, c := range cases {
		got, ok := ParsePublicJoinLink(c.raw)
		if ok != c.ok {
			t.Fatalf("%q ok=%v want %v", c.raw, ok, c.ok)
		}
		if !c.ok {
			continue
		}
		if got != c.target {
			t.Fatalf("%q => %q want %q", c.raw, got, c.target)
		}
	}
}

func TestParseMiniAppDeepLink(t *testing.T) {
	cases := []struct {
		raw       string
		username  string
		shortName string
		start     string
		ok        bool
	}{
		{
			raw:       "https://telegram.me/nmnmfunbot/panel?startapp=L3dlYi12ZXJpZnk%3D",
			username:  "nmnmfunbot",
			shortName: "panel",
			start:     "L3dlYi12ZXJpZnk=",
			ok:        true,
		},
		{
			raw:       "t.me/FooBot/verify?startapp=join_1",
			username:  "FooBot",
			shortName: "verify",
			start:     "join_1",
			ok:        true,
		},
		{
			raw:       "tg://resolve?domain=nmnmfunbot&appname=panel&startapp=x",
			username:  "nmnmfunbot",
			shortName: "panel",
			start:     "x",
			ok:        true,
		},
		{
			raw: "https://t.me/nmnmfunbot?start=joinverify_1",
			ok:  false,
		},
		{
			raw: "https://t.me/nmnmfunbot",
			ok:  false,
		},
	}
	for _, c := range cases {
		u, sn, s, ok := ParseMiniAppDeepLink(c.raw)
		if ok != c.ok {
			t.Fatalf("%q ok=%v want %v", c.raw, ok, c.ok)
		}
		if !c.ok {
			continue
		}
		if u != c.username || sn != c.shortName || s != c.start {
			t.Fatalf("%q => (%s,%s,%s) want (%s,%s,%s)", c.raw, u, sn, s, c.username, c.shortName, c.start)
		}
	}
}
