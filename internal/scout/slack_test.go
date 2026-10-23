package scout

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSlackTransport(t *testing.T) {
	slack := NewSlack("xoxb-test-secret", "C123")
	slack.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer xoxb-test-secret" || strings.Contains(r.URL.String(), "secret") {
			t.Fatal("token was missing or leaked into URL")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("channel") != "C123" || r.Form.Get("text") != "test & text" {
			t.Fatal("incorrect request encoding")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error":"not_in_channel"}`))}, nil
	})
	err := slack.Post(context.Background(), Notification{Text: "test & text"})
	if err == nil || err.Error() != "Slack: not_in_channel" {
		t.Fatalf("logical Slack error lost: %v", err)
	}
}

func TestSlackRetryAfter(t *testing.T) {
	slack := NewSlack("secret", "C123")
	slack.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"120"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	err := slack.Post(context.Background(), Notification{Text: "test"})
	if retryDelay(err, 1) != 120*time.Second {
		t.Fatalf("Retry-After ignored: %v", err)
	}
}

type fakeSlack struct {
	Pages       []HistoryPage
	Queries     []url.Values
	Posts       []string
	Sent        []Notification
	PostErr     error
	FetchErr    error
	IdentityErr error
}

func (f *fakeSlack) History(_ context.Context, args url.Values) (HistoryPage, error) {
	copyArgs, _ := url.ParseQuery(args.Encode())
	f.Queries = append(f.Queries, copyArgs)
	if f.FetchErr != nil {
		return HistoryPage{}, f.FetchErr
	}
	if len(f.Pages) == 0 {
		return HistoryPage{}, nil
	}
	page := f.Pages[0]
	f.Pages = f.Pages[1:]
	return page, nil
}

func (f *fakeSlack) Post(_ context.Context, n Notification) error {
	f.Posts = append(f.Posts, n.Text)
	f.Sent = append(f.Sent, n)
	return f.PostErr
}

func (f *fakeSlack) Identity(context.Context) (string, error) { return "B123", f.IdentityErr }

func TestHistoryPaginationAndDedup(t *testing.T) {
	d, _ := testDaemon(t)
	now := time.Now()
	older := slackTimestamp(now.Add(-2 * time.Second))
	newer := slackTimestamp(now.Add(-time.Second))
	one := Message{Type: "message", User: "U123", TS: newer, Text: "scout arm pectra gpu=0"}
	two := Message{Type: "message", User: "U123", TS: older, Text: "scout status pectra"}
	first := HistoryPage{Messages: []Message{one}, HasMore: true}
	first.Metadata.Cursor = "next"
	slack := &fakeSlack{Pages: []HistoryPage{first, {Messages: []Message{two}}}}
	d.Slack = slack
	d.State.Cursor = slackTimestamp(now.Add(-time.Minute))
	if err := d.poll(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(d.State.Watches) != 1 || len(d.State.Outbox) != 2 || d.State.Cursor != newer {
		t.Fatalf("history processing failed: %+v", d.State)
	}
	if slack.Queries[1].Get("cursor") != "next" {
		t.Fatal("pagination cursor not followed")
	}
	slack.Pages = []HistoryPage{{Messages: []Message{one}}}
	if err := d.poll(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(d.State.Outbox) != 2 {
		t.Fatal("replayed command created duplicate response")
	}
}

func TestColdStartAndFailedDelivery(t *testing.T) {
	d, _ := testDaemon(t)
	slack := &fakeSlack{PostErr: errors.New("network unavailable")}
	d.Slack = slack
	now := time.Now()
	if err := d.poll(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(slack.Queries) != 0 || d.State.Cursor == "" {
		t.Fatal("cold start read old history")
	}
	d.State.queue("event", "alert")
	if err := d.flush(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(d.State.Outbox) != 1 || d.State.Outbox[0].Attempts != 1 {
		t.Fatal("failed delivery was not retained")
	}
	slack.PostErr = nil
	if err := d.flush(context.Background(), d.State.Outbox[0].NextTry); err != nil {
		t.Fatal(err)
	}
	if len(d.State.Outbox) != 0 || len(slack.Posts) != 2 {
		t.Fatal("pending alert was not retried")
	}
}
