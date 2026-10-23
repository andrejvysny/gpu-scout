package scout

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIdentityFailureRetainsCursor(t *testing.T) {
	d, _ := testDaemon(t)
	now := time.Now()
	cursor := slackTimestamp(now.Add(-time.Minute))
	d.State.Cursor = cursor
	slack := &fakeSlack{IdentityErr: errors.New("identity unavailable")}
	d.Slack = slack
	if err := d.poll(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if d.State.Cursor != cursor || len(slack.Queries) != 0 {
		t.Fatal("failed identity lookup skipped unprocessed messages")
	}
}

func TestBacklogWindowAndMonotonicCursor(t *testing.T) {
	d, _ := testDaemon(t)
	now := time.Now()
	d.State.Cursor = slackTimestamp(now.Add(-24 * time.Hour))
	slack := &fakeSlack{Pages: []HistoryPage{{Messages: []Message{
		{Type: "message", User: "U123", TS: "invalid"},
		{Type: "message", User: "U123", TS: slackTimestamp(now.Add(-time.Hour))},
	}}}}
	d.Slack = slack
	if err := d.poll(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	cutoff := slackTimestamp(now.Add(-5 * time.Minute))
	if slack.Queries[0].Get("oldest") != cutoff || d.State.Cursor != cutoff {
		t.Fatalf("cursor regressed or stale backlog was requested: %s", d.State.Cursor)
	}
}

func TestRateLimitPausesWholeOutbox(t *testing.T) {
	d, _ := testDaemon(t)
	now := time.Now()
	slack := &fakeSlack{PostErr: &SlackError{Code: "ratelimited", RetryAfter: time.Minute}}
	d.Slack = slack
	d.State.queue("first", "one")
	d.State.queue("second", "two")
	if err := d.flush(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := d.flush(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(slack.Posts) != 1 || d.State.Outbox[1].NextTry.Before(now.Add(time.Minute)) {
		t.Fatal("another event bypassed Slack Retry-After")
	}
}
