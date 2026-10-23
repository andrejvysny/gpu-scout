package scout

import (
	"strings"
	"testing"
	"time"
)

func TestCommandValidation(t *testing.T) {
	tests := []struct {
		text      string
		addressed bool
		valid     bool
	}{
		{"scout arm pectra gpu=0", true, true},
		{"scout arm pectra gpu=GPU-first expires=24h", true, true},
		{"scout arm dencun gpu=0", false, true},
		{"scout arm pectra", true, false},
		{"scout arm pectra gpu=0 gpu=1", true, false},
		{"scout arm pectra gpu=0 expires=-1s", true, false},
		{"scout arm pectra gpu=0 surprise=yes", true, false},
		{"scout run pectra command=rm", true, false},
		{"scout cancel pectra watch=123", true, true},
		{"scout status pectra", true, true},
		{"ordinary channel message", false, true},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			_, addressed, err := parseCommand(test.text, "pectra")
			if addressed != test.addressed || (err == nil) != test.valid {
				t.Fatalf("addressed=%v error=%v", addressed, err)
			}
		})
	}
}

func TestWatchOwnershipAndRearm(t *testing.T) {
	d, _ := testDaemon(t)
	c := command{Action: "arm", GPU: "0"}
	d.arm(c, "U123", "first", time.Now())
	d.arm(c, "U123", "duplicate", time.Now())
	d.arm(c, "U456", "other-user", time.Now())
	if len(d.State.Watches) != 2 {
		t.Fatal("duplicate arm created a watch or separate users collided")
	}
	d.cancel("first", "U456")
	if len(d.State.Watches) != 2 {
		t.Fatal("cancelled another user's watch")
	}
	if strings.Contains(d.watches("U123").Text, "other-user") {
		t.Fatal("watch list leaked another user's watches")
	}
	d.cancel("first", "U123")
	if len(d.State.Watches) != 1 {
		t.Fatal("owner could not cancel")
	}
}

func TestIgnoredSlackMessages(t *testing.T) {
	base := Message{Type: "message", User: "U123", Text: "scout arm pectra gpu=0", TS: "1000.000001"}
	for _, name := range []string{"bot", "edit", "thread", "stale", "replayed", "other host", "invalid user"} {
		t.Run(name, func(t *testing.T) {
			d, _ := testDaemon(t)
			m := base
			now := time.Unix(1001, 0)
			switch name {
			case "bot":
				m.BotID = "B123"
			case "edit":
				m.Subtype = "message_changed"
			case "thread":
				m.ThreadTS = "999.000001"
			case "stale":
				now = time.Unix(2000, 0)
			case "replayed":
				d.State.Cursor = m.TS
			case "other host":
				m.Text = "scout arm dencun gpu=0"
			case "invalid user":
				m.User = "U123><!channel"
			}
			d.handle(m, now)
			if len(d.State.Watches) != 0 || len(d.State.Outbox) != 0 {
				t.Fatal("ignored message changed state")
			}
		})
	}
}
