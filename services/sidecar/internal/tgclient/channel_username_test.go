package tgclient

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestChannelPublicUsername(t *testing.T) {
	chLegacy := &tg.Channel{Username: "legacy"}
	if got := channelPublicUsername(chLegacy); got != "legacy" {
		t.Fatalf("legacy Username => %q", got)
	}

	chMulti := &tg.Channel{}
	chMulti.SetUsernames([]tg.Username{
		{Username: "inactive", Active: false},
		{Username: "gaotrx", Active: true},
	})
	if got := channelPublicUsername(chMulti); got != "gaotrx" {
		t.Fatalf("Usernames[] active => %q want gaotrx", got)
	}

	chInactiveOnly := &tg.Channel{}
	chInactiveOnly.SetUsernames([]tg.Username{{Username: "fallback", Active: false}})
	if got := channelPublicUsername(chInactiveOnly); got != "fallback" {
		t.Fatalf("inactive fallback => %q", got)
	}

	if got := channelPublicUsername(&tg.Channel{}); got != "" {
		t.Fatalf("empty => %q", got)
	}
}
