package scout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrappedRunExitAndDurableNotification(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{"success", []string{"/bin/sh", "-c", "exit 0"}, 0},
		{"failure", []string{"/bin/sh", "-c", "exit 7"}, 7},
		{"signal", []string{"/bin/sh", "-c", "kill -TERM $$"}, 143},
		{"missing command", []string{"/no/such/gpu-scout-command"}, 127},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, _ := testDaemon(t)
			code, _ := Run(d.Config, "train <danger>", "U123", test.args)
			if code != test.code {
				t.Fatalf("exit code %d, want %d", code, test.code)
			}
			if err := d.ingestRuns(); err != nil {
				t.Fatal(err)
			}
			if len(d.State.Outbox) != 1 || !strings.Contains(d.State.Outbox[0].Text, "train &lt;danger&gt;") {
				t.Fatal("completion missing or label was not escaped")
			}
			if err := d.ingestRuns(); err != nil || len(d.State.Outbox) != 1 {
				t.Fatal("run completion duplicated on ingestion")
			}
		})
	}
}

func TestRunPreservesArgumentsEnvironmentAndDirectory(t *testing.T) {
	d, _ := testDaemon(t)
	t.Setenv("GPU_SCOUT_TEST_VALUE", "kept")
	output := filepath.Join(t.TempDir(), "output")
	args := []string{"/bin/sh", "-c", `printf '%s\n%s\n%s' "$1" "$GPU_SCOUT_TEST_VALUE" "$PWD" > "$2"`, "sh", "space & literal $(never-run)", output}
	code, err := Run(d.Config, "arguments", "U123", args)
	if code != 0 || err != nil {
		t.Fatalf("run failed: %d %v", code, err)
	}
	data, err := os.ReadFile(output)
	if err != nil || !strings.HasPrefix(string(data), "space & literal $(never-run)\nkept\n") {
		t.Fatalf("arguments/environment changed: %q %v", data, err)
	}
}

func TestRunIngestionCrashRecovery(t *testing.T) {
	d, _ := testDaemon(t)
	event := RunEvent{ID: "test-event", Server: "pectra", User: "U123", Label: "train"}
	path := filepath.Join(d.Store.Dir, "runs", event.ID+".json")
	if err := writeJSON(path, event); err != nil {
		t.Fatal(err)
	}
	d.State.SeenRuns[event.ID] = true
	d.State.queue("run-test-event", "already queued before crash")
	if err := d.ingestRun(event.ID + ".json"); err != nil {
		t.Fatal(err)
	}
	if len(d.State.Outbox) != 1 {
		t.Fatal("already-queued run was queued again")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("ingested event was not removed")
	}
}

func TestStatePermissionsAndCorruption(t *testing.T) {
	d, _ := testDaemon(t)
	if err := d.Store.Save(d.State); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d.Store.Dir, "state.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("state file permissions are not 0600")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Store.Load(d.Config); err == nil {
		t.Fatal("corrupt state silently discarded")
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data, _ := json.Marshal(map[string]string{"server": "pectra", "slack_channel": "C123", "state_dir": t.TempDir()})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil || c.MaxMemoryMiB != 512 || c.IdlePeriod != "2m" {
		t.Fatalf("invalid defaults: %+v %v", c, err)
	}
	c.GPUPoll = "0s"
	if c.validate() == nil {
		t.Fatal("accepted a non-positive polling interval")
	}
}
