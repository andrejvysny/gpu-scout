package scout

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Daemon struct {
	Config     Config
	State      State
	Store      *Store
	Slack      Messenger
	GPUs       GPUReader
	lastSample time.Time
	nextPoll   time.Time
	botID      string
}

func (d *Daemon) Serve(ctx context.Context) error {
	if err := d.poll(ctx, time.Now()); err != nil {
		return err
	}
	d.sample(time.Now())
	if err := d.Store.Save(d.State); err != nil {
		return err
	}
	slog.Info("GPU Scout started", "server", d.Config.Server, "slack_poll", d.Config.SlackPoll, "gpu_poll", d.Config.GPUPoll)
	poll := time.NewTicker(duration(d.Config.SlackPoll))
	sample := time.NewTicker(duration(d.Config.GPUPoll))
	flush := time.NewTicker(time.Second)
	defer poll.Stop()
	defer sample.Stop()
	defer flush.Stop()
	for {
		var err error
		select {
		case <-ctx.Done():
			return nil
		case now := <-poll.C:
			err = d.poll(ctx, now)
		case <-sample.C:
			if len(d.State.Watches) == 0 {
				continue
			}
			d.sample(time.Now())
			err = d.Store.Save(d.State)
		case <-flush.C:
			err = d.flush(ctx, time.Now())
		}
		if err != nil {
			return err
		}
	}
}

func (d *Daemon) poll(ctx context.Context, now time.Time) error {
	if err := d.ingestRuns(); err != nil {
		return err
	}
	if err := d.ingestRequests(now); err != nil {
		return err
	}
	if now.Before(d.nextPoll) {
		return nil
	}
	if d.State.Cursor == "" {
		// First installation must not execute old commands from channel history.
		d.State.Cursor = slackTimestamp(now)
		return d.Store.Save(d.State)
	}
	cutoff := slackTimestamp(now.Add(-5 * time.Minute))
	if timestampBefore(d.State.Cursor, cutoff) {
		d.State.Cursor = cutoff
		if err := d.Store.Save(d.State); err != nil {
			return err
		}
	}
	messages, err := d.fetch(ctx)
	if err != nil {
		slog.Warn("Slack history unavailable", "error", err)
		d.nextPoll = now.Add(retryDelay(err, 1))
		return nil
	}
	for _, m := range messages {
		if !timestampBefore(d.State.Cursor, m.TS) {
			continue
		}
		d.handle(m, time.Now())
		d.State.Cursor = m.TS
		if err := d.Store.Save(d.State); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) fetch(ctx context.Context) ([]Message, error) {
	if d.botID == "" {
		var err error
		d.botID, err = d.Slack.Identity(ctx)
		if err != nil {
			return nil, err
		}
	}
	args := url.Values{"oldest": {d.State.Cursor}, "latest": {slackTimestamp(time.Now())}, "inclusive": {"false"}, "include_all_metadata": {"true"}}
	var messages []Message
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		page, err := d.Slack.History(ctx, args)
		if err != nil {
			return nil, err
		}
		messages = append(messages, page.Messages...)
		if page.Metadata.Cursor != "" {
			args.Set("cursor", page.Metadata.Cursor)
			continue
		}
		if page.HasMore {
			return nil, errors.New("Slack history pagination incomplete")
		}
		sort.Slice(messages, func(i, j int) bool { return timestampBefore(messages[i].TS, messages[j].TS) })
		return messages, nil
	}
	return nil, errors.New("Slack history exceeds 100 pages; cursor retained")
}

func (d *Daemon) handle(m Message, now time.Time) {
	m, requestID, accepted := d.commandMessage(m, now)
	if !accepted || !timestampBefore(d.State.Cursor, m.TS) || m.Type != "message" || !userPattern.MatchString(m.User) {
		return
	}
	if m.ThreadTS != "" && m.ThreadTS != m.TS {
		return
	}
	when, err := parseTimestamp(m.TS)
	if err != nil || now.Sub(when) > 5*time.Minute || when.After(now.Add(time.Minute)) {
		return
	}
	c, addressed, err := parseCommand(m.Text, d.Config.Server)
	if !addressed {
		return
	}
	id := strings.ReplaceAll(m.TS, ".", "-")
	if requestID != "" {
		id = "slash-" + requestID[:16]
	}
	var reply Reply
	if err != nil {
		reply = infoReply("Invalid command", escape(err.Error()))
	} else {
		reply = d.execute(c, m.User, id, now)
	}
	if requestID != "" {
		d.State.remember("execute-"+requestID, now)
	}
	d.State.queueReply("command-"+id, reply.forUser(m.User))
}

func (d *Daemon) flush(ctx context.Context, now time.Time) error {
	for i, n := range d.State.Outbox {
		if now.Before(n.NextTry) {
			continue
		}
		n = expireRelay(n, now)
		if err := d.Slack.Post(ctx, n); err != nil {
			n.Attempts++
			n.NextTry = time.Now().Add(retryDelay(err, n.Attempts))
			d.State.Outbox[i] = n
			slog.Warn("Slack delivery pending", "event", n.ID, "error", err)
			var slackErr *SlackError
			if errors.As(err, &slackErr) && slackErr.Code == "ratelimited" {
				for j := range d.State.Outbox {
					if d.State.Outbox[j].NextTry.Before(n.NextTry) {
						d.State.Outbox[j].NextTry = n.NextTry
					}
				}
			}
		} else {
			d.State.Outbox = append(d.State.Outbox[:i], d.State.Outbox[i+1:]...)
		}
		return d.Store.Save(d.State)
	}
	return nil
}

func retryDelay(err error, attempts int) time.Duration {
	delay := min(30*time.Second*time.Duration(1<<min(attempts-1, 6)), 30*time.Minute)
	var slackErr *SlackError
	if errors.As(err, &slackErr) {
		delay = max(delay, slackErr.RetryAfter)
	}
	return delay
}

func slackTimestamp(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

func parseTimestamp(s string) (time.Time, error) {
	seconds, fraction, ok := strings.Cut(s, ".")
	if !ok || len(fraction) != 6 {
		return time.Time{}, errors.New("invalid Slack timestamp")
	}
	sec, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	microseconds, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil || microseconds < 0 || microseconds > 999999 {
		return time.Time{}, errors.New("invalid Slack timestamp fraction")
	}
	return time.Unix(sec, microseconds*1000), nil
}

func timestampBefore(a, b string) bool {
	bTime, err := parseTimestamp(b)
	if err != nil {
		return false
	}
	if a == "" {
		return true
	}
	aTime, err := parseTimestamp(a)
	return err == nil && aTime.Before(bTime)
}
