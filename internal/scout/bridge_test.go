package scout

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlashValidationAndHelp(t *testing.T) {
	d, _ := testDaemon(t)
	for _, test := range []struct {
		text, channel string
		queued        bool
	}{
		{"arm dencun gpu=0", "C123", true}, {"status pectra", "C123", true},
		{"arm dencun", "C123", false}, {"arm unknown gpu=0", "C123", false},
		{"status pectra", "C999", false}, {"", "C123", false}, {"help", "C123", false},
	} {
		t.Run(test.text+test.channel, func(t *testing.T) {
			_, reply, queued := prepareSlash(d.Config, SlashCommand{Command: "/scout", Text: test.text, ChannelID: test.channel, UserID: "U123", TriggerID: "trigger"}, time.Now())
			if queued != test.queued || len(reply.Blocks) == 0 {
				t.Fatalf("unexpected result: queued=%v reply=%+v", queued, reply)
			}
		})
	}
}

func TestSlashSpoolIsDurableBeforeAck(t *testing.T) {
	d, _ := testDaemon(t)
	r := SocketReceiver{Config: d.Config, Store: d.Store}
	envelope := socketEnvelope{ID: "envelope", AcceptsReply: true, Payload: SlashCommand{Command: "/scout", Text: "arm dencun gpu=0", ChannelID: "C123", UserID: "U123", TriggerID: "same-trigger"}}
	ack := r.accept(envelope, time.Now())
	if ack.Payload == nil || !strings.Contains(ack.Payload.Text, "queued") {
		t.Fatal("slash request was not acknowledged")
	}
	entries, err := os.ReadDir(filepath.Join(d.Store.Dir, "requests"))
	if err != nil || len(entries) != 1 {
		t.Fatal("acknowledged request was not saved")
	}
	if err := d.ingestRequests(time.Now()); err != nil {
		t.Fatal(err)
	}
	r.accept(envelope, time.Now())
	if err := d.ingestRequests(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(d.State.Outbox) != 1 || d.State.Outbox[0].Metadata == nil {
		t.Fatal("receiver retry duplicated relay")
	}
	r.Store = &Store{Dir: filepath.Join(d.Store.Dir, "missing")}
	ack = r.accept(envelope, time.Now())
	if !strings.Contains(ack.Payload.Text, "not saved") {
		t.Fatal("failed storage was acknowledged as queued")
	}
}

func TestCrossHostRelayAndDuplicateExecution(t *testing.T) {
	receiver, _ := testDaemon(t)
	receiver.Config.Server = "dencun"
	receiver.State.Server = "dencun"
	now := time.Now()
	request, _, _ := prepareSlash(receiver.Config, SlashCommand{Command: "/scout", Text: "arm pectra gpu=0", ChannelID: "C123", UserID: "U456", TriggerID: "cross-host"}, now)
	if err := writeJSON(filepath.Join(receiver.Store.Dir, "requests", request.ID+".json"), request); err != nil {
		t.Fatal(err)
	}
	if err := receiver.ingestRequests(now); err != nil {
		t.Fatal(err)
	}
	target, _ := testDaemon(t)
	target.botID = "B123"
	m := Message{Type: "message", Subtype: "bot_message", BotID: "B123", User: "UBOT", TS: slackTimestamp(now), Metadata: receiver.State.Outbox[0].Metadata}
	target.handle(m, now)
	m.TS = slackTimestamp(now.Add(time.Second))
	target.handle(m, now.Add(time.Second))
	if len(target.State.Watches) != 1 || target.State.Watches[0].User != "U456" || len(target.State.Outbox) != 1 {
		t.Fatal("relay target, requester or deduplication failed")
	}
	if err := target.Store.Save(target.State); err != nil {
		t.Fatal(err)
	}
	state, err := target.Store.Load(target.Config)
	if err != nil {
		t.Fatal(err)
	}
	target.State = state
	target.handle(m, now.Add(2*time.Second))
	if len(target.State.Outbox) != 1 {
		t.Fatal("deduplication did not survive restart")
	}
}

func TestSpoofedAndStaleBotCommandsRejected(t *testing.T) {
	for _, fault := range []string{"other bot", "missing identity", "stale", "invalid id", "replayed"} {
		t.Run(fault, func(t *testing.T) {
			d, _ := testDaemon(t)
			d.botID = "B123"
			now := time.Now()
			req, _, _ := prepareSlash(d.Config, SlashCommand{Command: "/scout", Text: "arm pectra gpu=0", ChannelID: "C123", UserID: "U123", TriggerID: "test"}, now)
			m := Message{Type: "message", BotID: "B123", TS: slackTimestamp(now), Metadata: &CommandMetadata{Type: "gpu_scout_command", Data: req}}
			switch fault {
			case "other bot":
				m.BotID = "BOTHER"
			case "missing identity":
				d.botID = ""
			case "stale":
				m.Metadata.Data.RequestedAt = now.Add(-6 * time.Minute)
			case "invalid id":
				m.Metadata.Data.ID = "bad"
			case "replayed":
				d.State.remember("execute-"+req.ID, now)
			}
			d.handle(m, now)
			if len(d.State.Watches) != 0 || len(d.State.Outbox) != 0 {
				t.Fatal("untrusted relay changed state")
			}
		})
	}
}

func TestDelayedRelayBecomesExpiryNotice(t *testing.T) {
	d, _ := testDaemon(t)
	now := time.Now()
	req, _, _ := prepareSlash(d.Config, SlashCommand{Command: "/scout", Text: "status pectra", ChannelID: "C123", UserID: "U123", TriggerID: "stale"}, now.Add(-6*time.Minute))
	d.State.Outbox = []Notification{{ID: "relay", Metadata: &CommandMetadata{Type: "gpu_scout_command", Data: req}}}
	slack := &fakeSlack{}
	d.Slack = slack
	if err := d.flush(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if slack.Sent[0].Metadata != nil || !strings.Contains(slack.Posts[0], "expired") {
		t.Fatal("expired relay was still dispatched")
	}
}

func TestHumanMetadataCannotImpersonateRequester(t *testing.T) {
	d, _ := testDaemon(t)
	now := time.Now()
	req, _, _ := prepareSlash(d.Config, SlashCommand{Command: "/scout", Text: "arm pectra gpu=0", ChannelID: "C123", UserID: "U456", TriggerID: "spoof"}, now)
	m := Message{Type: "message", User: "U123", TS: slackTimestamp(now), Text: "scout arm pectra gpu=0", Metadata: &CommandMetadata{Type: "gpu_scout_command", Data: req}}
	d.handle(m, now)
	if len(d.State.Watches) != 1 || d.State.Watches[0].User != "U123" {
		t.Fatal("human metadata changed command ownership")
	}
}
