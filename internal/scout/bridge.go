package scout

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var requestIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type SlashCommand struct {
	Command   string `json:"command"`
	Text      string `json:"text"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	TriggerID string `json:"trigger_id"`
}

type SlashRequest struct {
	ID          string    `json:"id"`
	User        string    `json:"user"`
	Text        string    `json:"text"`
	RequestedAt time.Time `json:"requested_at"`
}

type CommandMetadata struct {
	Type string       `json:"event_type"`
	Data SlashRequest `json:"event_payload"`
}

func prepareSlash(c Config, cmd SlashCommand, now time.Time) (SlashRequest, Reply, bool) {
	if cmd.ChannelID != c.SlackChannel {
		return SlashRequest{}, infoReply("Wrong channel", "Use GPU Scout's configured channel."), false
	}
	if cmd.Command != "/scout" || !userPattern.MatchString(cmd.UserID) {
		return SlashRequest{}, infoReply("Invalid request", "Use `/scout help` for usage."), false
	}
	parts := strings.Fields(cmd.Text)
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "help") {
		return SlashRequest{}, helpReply(cmd.UserID), false
	}
	if len(parts) < 2 || (parts[1] != "dencun" && parts[1] != "pectra") {
		return SlashRequest{}, infoReply("Choose a server", "Use `dencun` or `pectra`, for example `/scout status dencun`. Use `/scout help` for all commands."), false
	}
	text := "scout " + strings.Join(parts, " ")
	if _, _, err := parseCommand(text, parts[1]); err != nil {
		return SlashRequest{}, infoReply("Invalid command", escape(err.Error())), false
	}
	if cmd.TriggerID == "" {
		return SlashRequest{}, infoReply("Invalid request", "Slack request identifier is missing. Please retry."), false
	}
	hash := sha256.Sum256([]byte(cmd.TriggerID))
	req := SlashRequest{ID: hex.EncodeToString(hash[:]), User: cmd.UserID, Text: text, RequestedAt: now.UTC()}
	return req, infoReply("Request queued", "Results normally appear in this channel within about 30 seconds. A watch is armed only after its confirmation reply."), true
}

func (d *Daemon) ingestRequests(now time.Time) error {
	for id, seen := range d.State.SeenCommands {
		if now.Sub(seen) > 24*time.Hour {
			delete(d.State.SeenCommands, id)
		}
	}
	entries, err := os.ReadDir(filepath.Join(d.Store.Dir, "requests"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := d.ingestRequest(entry.Name(), now); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) ingestRequest(name string, now time.Time) error {
	path := filepath.Join(d.Store.Dir, "requests", name)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var req SlashRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("invalid slash request: %w", err)
	}
	if req.ID+".json" != name || !requestIDPattern.MatchString(req.ID) || !userPattern.MatchString(req.User) {
		return fmt.Errorf("invalid slash request identity: %s", name)
	}
	if _, seen := d.State.SeenCommands["relay-"+req.ID]; !seen {
		n := Notification{ID: "relay-" + req.ID, Text: fmt.Sprintf("<@%s> requested /%s", req.User, req.Text), Metadata: &CommandMetadata{Type: "gpu_scout_command", Data: req}}
		n.Blocks = []Block{contextBlock(n.Text)}
		d.State.Outbox = append(d.State.Outbox, expireRelay(n, now))
		d.State.remember("relay-"+req.ID, now)
		if err := d.Store.Save(d.State); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

func expireRelay(n Notification, now time.Time) Notification {
	if n.Metadata == nil || now.Sub(n.Metadata.Data.RequestedAt) <= 5*time.Minute {
		return n
	}
	r := infoReply("Request expired", "Dispatch was delayed for more than 5 minutes. Please retry the slash command.").forUser(n.Metadata.Data.User)
	n.Text, n.Blocks, n.Metadata = r.Text, r.Blocks, nil
	return n
}

func (d *Daemon) commandMessage(m Message, now time.Time) (Message, string, bool) {
	if m.BotID == "" {
		return m, "", m.Subtype == ""
	}
	if d.botID == "" || m.BotID != d.botID || m.Metadata == nil || m.Metadata.Type != "gpu_scout_command" {
		return m, "", false
	}
	req := m.Metadata.Data
	if !requestIDPattern.MatchString(req.ID) || !userPattern.MatchString(req.User) || req.RequestedAt.After(now.Add(time.Minute)) || now.Sub(req.RequestedAt) > 5*time.Minute {
		return m, "", false
	}
	if _, seen := d.State.SeenCommands["execute-"+req.ID]; seen {
		return m, "", false
	}
	m.User, m.Text, m.Subtype = req.User, req.Text, ""
	return m, req.ID, true
}
