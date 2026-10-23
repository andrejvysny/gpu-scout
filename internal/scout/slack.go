package scout

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Type     string           `json:"type"`
	Subtype  string           `json:"subtype"`
	User     string           `json:"user"`
	BotID    string           `json:"bot_id"`
	Text     string           `json:"text"`
	TS       string           `json:"ts"`
	ThreadTS string           `json:"thread_ts"`
	Metadata *CommandMetadata `json:"metadata,omitempty"`
}

type HistoryPage struct {
	Messages []Message `json:"messages"`
	HasMore  bool      `json:"has_more"`
	Metadata struct {
		Cursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

type Messenger interface {
	History(context.Context, url.Values) (HistoryPage, error)
	Post(context.Context, Notification) error
	Identity(context.Context) (string, error)
}

type Slack struct {
	baseURL string
	token   string
	channel string
	client  *http.Client
}

type SlackError struct {
	Code       string
	RetryAfter time.Duration
}

func (e *SlackError) Error() string { return "Slack: " + e.Code }

func NewSlack(token, channel string) *Slack {
	client := &http.Client{Timeout: 10 * time.Second}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Slack{baseURL: "https://slack.com/api/", token: token, channel: channel, client: client}
}

func (s *Slack) History(ctx context.Context, args url.Values) (HistoryPage, error) {
	args.Set("channel", s.channel)
	if args.Get("limit") == "" {
		args.Set("limit", "100")
	}
	var page HistoryPage
	err := s.call(ctx, "conversations.history", args, &page)
	return page, err
}

func (s *Slack) Post(ctx context.Context, n Notification) error {
	args := url.Values{"channel": {s.channel}, "text": {n.Text}, "unfurl_links": {"false"}, "unfurl_media": {"false"}}
	if len(n.Blocks) != 0 {
		data, err := json.Marshal(n.Blocks)
		if err != nil {
			return err
		}
		args.Set("blocks", string(data))
	}
	if n.Metadata != nil {
		data, err := json.Marshal(n.Metadata)
		if err != nil {
			return err
		}
		args.Set("metadata", string(data))
	}
	return s.call(ctx, "chat.postMessage", args, nil)
}

func (s *Slack) Identity(ctx context.Context) (string, error) {
	var identity struct {
		BotID string `json:"bot_id"`
	}
	if err := s.call(ctx, "auth.test", nil, &identity); err != nil {
		return "", err
	}
	if identity.BotID == "" {
		return "", fmt.Errorf("Slack bot identity is missing")
	}
	return identity.BotID, nil
}

func (s *Slack) Check(ctx context.Context) error {
	if _, err := s.Identity(ctx); err != nil {
		return err
	}
	_, err := s.History(ctx, url.Values{"limit": {"1"}})
	return err
}

func (s *Slack) call(ctx context.Context, method string, args url.Values, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+method, strings.NewReader(args.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("Slack %s request failed: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return &SlackError{Code: "ratelimited", RetryAfter: time.Duration(max(seconds, 1)) * time.Second}
	}
	if resp.StatusCode != http.StatusOK {
		return &SlackError{Code: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return fmt.Errorf("Slack %s response read failed: %w", method, err)
	}
	return decodeSlack(data, target)
}

func decodeSlack(data []byte, target interface{}) error {
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("invalid Slack response: %w", err)
	}
	if !result.OK {
		return &SlackError{Code: result.Error}
	}
	if target != nil {
		return json.Unmarshal(data, target)
	}
	return nil
}
