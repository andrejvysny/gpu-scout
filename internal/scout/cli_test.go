package scout

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIWithoutConfig(t *testing.T) {
	for _, command := range []string{"help", "version"} {
		var out, stderr bytes.Buffer
		code := CLI([]string{"-config", "/missing/config", command}, &out, &stderr)
		if code != 0 || out.Len() == 0 || stderr.Len() != 0 {
			t.Fatalf("%s: exit=%d output=%q error=%q", command, code, out.String(), stderr.String())
		}
	}
}

func TestDoctorDoesNotSendSlackMessages(t *testing.T) {
	t.Setenv("SLACK_APP_TOKEN", "")
	d, gpu := testDaemon(t)
	slack := NewSlack("xoxb-fake", "C123")
	calls := 0
	slack.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/auth.test") && !strings.HasSuffix(r.URL.Path, "/conversations.history") {
			t.Fatal("doctor attempted a Slack write")
		}
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"bot_id":"B123","messages":[]}`))}, nil
	})
	var out bytes.Buffer
	code, err := doctor(context.Background(), d.Config, slack, gpu, &out)
	if code != 0 || err != nil || calls != 2 || out.Len() == 0 {
		t.Fatalf("doctor failed: code=%d error=%v calls=%d", code, err, calls)
	}
}

func TestCLIRunReturnsChildExitCode(t *testing.T) {
	d, _ := testDaemon(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(d.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	args := []string{"-config", configPath, "run", "--label", "CLI test", "--notify-user", "U123", "--", "/bin/sh", "-c", "exit 23"}
	if code := CLI(args, &out, &stderr); code != 23 {
		t.Fatalf("wrong wrapper exit: %d; %s", code, stderr.String())
	}
	if err := d.ingestRuns(); err != nil || len(d.State.Outbox) != 1 {
		t.Fatal("CLI run did not queue completion")
	}
}
