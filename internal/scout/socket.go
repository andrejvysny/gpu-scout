package scout

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type socketEnvelope struct {
	Type         string       `json:"type"`
	ID           string       `json:"envelope_id"`
	AcceptsReply bool         `json:"accepts_response_payload"`
	Payload      SlashCommand `json:"payload"`
}

type socketReply struct {
	ResponseType string  `json:"response_type"`
	Text         string  `json:"text"`
	Blocks       []Block `json:"blocks,omitempty"`
}

type socketAck struct {
	ID      string       `json:"envelope_id"`
	Payload *socketReply `json:"payload,omitempty"`
}

type SocketReceiver struct {
	Slack    *Slack
	AppToken string
	Config   Config
	Store    *Store
}

func (r *SocketReceiver) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := r.session(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("Slack slash receiver reconnecting", "error", err)
		timer := time.NewTimer(retryDelay(err, 1))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *SocketReceiver) socketURL(ctx context.Context) (string, error) {
	if !strings.HasPrefix(r.AppToken, "xapp-") {
		return "", errors.New("SLACK_APP_TOKEN must be an app-level xapp- token with connections:write")
	}
	app := NewSlack(r.AppToken, r.Config.SlackChannel)
	app.baseURL, app.client = r.Slack.baseURL, r.Slack.client
	var result struct {
		URL string `json:"url"`
	}
	if err := app.call(ctx, "apps.connections.open", nil, &result); err != nil {
		return "", err
	}
	u, err := url.Parse(result.URL)
	if err != nil || u.Scheme != "wss" || !strings.HasSuffix(u.Hostname(), ".slack.com") || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return "", errors.New("Slack returned an invalid Socket Mode endpoint")
	}
	return result.URL, nil
}

func (r *SocketReceiver) session(ctx context.Context) error {
	endpoint, err := r.socketURL(ctx)
	if err != nil {
		return err
	}
	dialContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, _, err := websocket.Dial(dialContext, endpoint, &websocket.DialOptions{HTTPClient: r.Slack.client})
	cancel()
	if err != nil {
		return errors.New("Slack Socket Mode connection failed; check network access")
	}
	defer conn.CloseNow()
	conn.SetReadLimit(128 * 1024)
	slog.Info("Slack slash receiver connected", "server", r.Config.Server)
	return r.read(ctx, conn)
}

func (r *SocketReceiver) read(ctx context.Context, conn *websocket.Conn) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return errors.New("Slack Socket Mode connection closed")
		}
		var envelope socketEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			return errors.New("invalid Slack socket envelope")
		}
		if envelope.Type == "disconnect" {
			return errors.New("Slack requested reconnection")
		}
		if envelope.Type != "slash_commands" || envelope.ID == "" {
			continue
		}
		ack := r.accept(envelope, time.Now())
		encoded, err := json.Marshal(ack)
		if err != nil {
			return err
		}
		writeContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = conn.Write(writeContext, websocket.MessageText, encoded)
		cancel()
		if err != nil {
			return errors.New("Slack slash acknowledgment failed")
		}
	}
}

func (r *SocketReceiver) accept(envelope socketEnvelope, now time.Time) socketAck {
	req, reply, queued := prepareSlash(r.Config, envelope.Payload, now)
	if queued {
		if err := writeJSON(filepath.Join(r.Store.Dir, "requests", req.ID+".json"), req); err != nil {
			slog.Error("Could not persist slash request", "error", err)
			reply = infoReply("Request not saved", "The command was not queued. Please retry after the service storage problem is resolved.")
		}
	}
	ack := socketAck{ID: envelope.ID}
	if envelope.AcceptsReply {
		ack.Payload = &socketReply{ResponseType: "ephemeral", Text: reply.Text, Blocks: reply.Blocks}
	}
	return ack
}
