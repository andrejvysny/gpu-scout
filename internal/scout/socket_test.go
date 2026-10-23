package scout

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSocketEndpointAndAppToken(t *testing.T) {
	for _, endpoint := range []string{"wss://wss-primary.slack.com/link?ticket=secret", "wss://evil.example/link?ticket=secret", "https://wss-primary.slack.com/link?ticket=secret"} {
		t.Run(strings.Split(endpoint, "?")[0], func(t *testing.T) {
			d, _ := testDaemon(t)
			slack := NewSlack("xoxb-fake", "C123")
			slack.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer xapp-fake" {
					t.Fatal("wrong token used to open socket")
				}
				body, _ := json.Marshal(map[string]interface{}{"ok": true, "url": endpoint})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})
			receiver := SocketReceiver{Slack: slack, AppToken: "xapp-fake", Config: d.Config}
			_, err := receiver.socketURL(context.Background())
			if (err == nil) != strings.HasPrefix(endpoint, "wss://wss-primary.slack.com/") {
				t.Fatalf("unexpected endpoint validation: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("socket ticket leaked in error")
			}
		})
	}
}

func TestSocketAcknowledgesAfterDurableSave(t *testing.T) {
	d, _ := testDaemon(t)
	receiver := SocketReceiver{Config: d.Config, Store: d.Store}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer conn.CloseNow()
		envelope := socketEnvelope{Type: "slash_commands", ID: "envelope", AcceptsReply: true, Payload: SlashCommand{Command: "/scout", Text: "status pectra", ChannelID: "C123", UserID: "U123", TriggerID: "wire-test"}}
		data, _ := json.Marshal(envelope)
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			result <- err
			return
		}
		_, ackData, err := conn.Read(ctx)
		if err != nil {
			result <- err
			return
		}
		var ack socketAck
		if err := json.Unmarshal(ackData, &ack); err != nil {
			result <- err
			return
		}
		entries, err := os.ReadDir(filepath.Join(d.Store.Dir, "requests"))
		if err == nil && (len(entries) != 1 || ack.ID != "envelope" || ack.Payload == nil || ack.Payload.ResponseType != "ephemeral") {
			err = io.ErrUnexpectedEOF
		}
		result <- err
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	go func() { _ = receiver.read(ctx, conn) }()
	if err := <-result; err != nil {
		t.Fatalf("socket exchange failed: %v", err)
	}
}
