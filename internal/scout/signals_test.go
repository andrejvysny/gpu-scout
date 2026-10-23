package scout

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestWrapperForwardsTermination(t *testing.T) {
	d, _ := testDaemon(t)
	ready := filepath.Join(t.TempDir(), "ready")
	helper := exec.Command(os.Args[0], "-test.run=^TestSignalHelper$")
	helper.Env = append(os.Environ(), "GPU_SCOUT_SIGNAL_HELPER=1", "GPU_SCOUT_TEST_STATE="+d.Config.StateDir, "GPU_SCOUT_TEST_READY="+ready)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		data, _ := os.ReadFile(ready)
		if pid, err := strconv.Atoi(string(data)); err == nil {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		_ = helper.Process.Kill()
	})
	if err := waitForFile(ready); err != nil {
		t.Fatal(err)
	}
	if err := helper.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := helper.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("child trap did not receive forwarded SIGTERM: %v", err)
	}
	if err := d.ingestRuns(); err != nil || len(d.State.Outbox) != 1 {
		t.Fatalf("signal exit was not queued: %v", err)
	}
}

func waitForFile(path string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) != 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("child did not become ready for signal test")
}

func TestSignalHelper(t *testing.T) {
	if os.Getenv("GPU_SCOUT_SIGNAL_HELPER") != "1" {
		return
	}
	c := Config{Server: "pectra", StateDir: os.Getenv("GPU_SCOUT_TEST_STATE")}
	args := []string{"/bin/sh", "-c", `trap 'exit 42' TERM; printf '%s' "$$" > "$1"; while :; do sleep 1; done`, "sh", os.Getenv("GPU_SCOUT_TEST_READY")}
	code, err := Run(c, "signal test", "U123", args)
	if err != nil {
		os.Exit(99)
	}
	os.Exit(code)
}
