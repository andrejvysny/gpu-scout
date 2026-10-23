package scout

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStatusCardsSeparateMetricsAndIdentity(t *testing.T) {
	d, gpu := testDaemon(t)
	gpu.GPUs = append(gpu.GPUs, GPU{Index: 1, UUID: "GPU-second", Name: "NVIDIA RTX PRO 6000", ComputeCount: 1, MemoryUsed: 90 << 30, MemoryTotal: 96 << 30})
	r := d.status()
	if r.Blocks[0].Type != "header" {
		t.Fatal("status lacks header")
	}
	states := []string{}
	for _, b := range r.Blocks {
		if b.Type == "section" {
			if strings.Contains(b.Text.Text, "GPU-first") {
				t.Fatal("UUID is mixed into primary GPU title")
			}
			if len(b.Fields) != 4 {
				t.Fatal("GPU metrics are not separated into four fields")
			}
			states = append(states, b.Fields[0].Text)
		}
	}
	if len(states) != 2 || !strings.Contains(states[0], "Idle now") || !strings.Contains(states[1], "In use") {
		t.Fatal("state labels are wrong")
	}
	assertBlocks(t, r.Blocks)
}

func TestHelpAndWatchCards(t *testing.T) {
	d, _ := testDaemon(t)
	r := d.arm(command{Action: "arm", GPU: "0", Expiry: time.Hour}, "U123", "watch", time.Now())
	if r.Blocks[0].Text.Text != "Monitor armed" || len(r.Blocks[1].Fields) != 4 {
		t.Fatal("watch confirmation is not structured")
	}
	help := helpReply("U123")
	data, _ := json.Marshal(help.Blocks)
	for _, required := range []string{"/scout status", "/scout arm", "/scout watches", "/scout cancel", "--notify-user U123", "Bookings"} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("help lacks %q", required)
		}
	}
	assertBlocks(t, help.Blocks)
	assertBlocks(t, r.Blocks)
}

func assertBlocks(t *testing.T, blocks []Block) {
	t.Helper()
	if len(blocks) == 0 || len(blocks) > 50 {
		t.Fatal("invalid block count")
	}
	for _, b := range blocks {
		if b.Text != nil && ((b.Type == "header" && len([]rune(b.Text.Text)) > 150) || len([]rune(b.Text.Text)) > 3000) {
			t.Fatal("Slack text limit exceeded")
		}
		if len(b.Fields) > 10 || len(b.Elements) > 10 {
			t.Fatal("Slack field limit exceeded")
		}
	}
}

func TestRichSlackTransport(t *testing.T) {
	slack := NewSlack("secret", "C123")
	req := SlashRequest{ID: strings.Repeat("a", 64), User: "U123", Text: "scout status pectra", RequestedAt: time.Now()}
	slack.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		var blocks []Block
		var meta CommandMetadata
		if err := json.Unmarshal([]byte(r.Form.Get("blocks")), &blocks); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(r.Form.Get("metadata")), &meta); err != nil {
			t.Fatal(err)
		}
		if meta.Data.ID != req.ID || blocks[0].Type != "header" {
			t.Fatal("rich payload changed in transport")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})
	n := Notification{Text: "fallback", Blocks: []Block{header("GPU status")}, Metadata: &CommandMetadata{Type: "gpu_scout_command", Data: req}}
	if err := slack.Post(context.Background(), n); err != nil {
		t.Fatal(err)
	}
}
