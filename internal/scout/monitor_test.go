package scout

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeGPU struct {
	GPUs []GPU
	Err  error
}

func (f *fakeGPU) Snapshot() ([]GPU, error) { return f.GPUs, f.Err }
func (f *fakeGPU) Close()                   {}

func testDaemon(t *testing.T) (*Daemon, *fakeGPU) {
	t.Helper()
	c := Config{Server: "pectra", SlackChannel: "C123", StateDir: t.TempDir(), SlackPoll: "15s", GPUPoll: "30s", IdlePeriod: "2m", MaxUtilization: 5, MaxMemoryMiB: 512}
	store, err := NewStore(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	gpus := &fakeGPU{GPUs: []GPU{{Index: 0, UUID: "GPU-first", Name: "Test GPU", MemoryTotal: 24 << 30}}}
	d := &Daemon{Config: c, Store: store, GPUs: gpus, State: State{Server: c.Server, Channel: c.SlackChannel, SeenRuns: map[string]bool{}}}
	return d, gpus
}

func armTestWatch(d *Daemon) {
	d.State.Watches = []Watch{{ID: "watch1", UUID: "GPU-first", User: "U123"}}
}

func TestIdleConfirmationAndOneShot(t *testing.T) {
	d, gpu := testDaemon(t)
	gpu.GPUs[0].Utilization = 5
	gpu.GPUs[0].MemoryUsed = 512 << 20
	armTestWatch(d)
	start := time.Unix(1000, 0)
	for seconds := 0; seconds < 120; seconds += 30 {
		d.sample(start.Add(time.Duration(seconds) * time.Second))
		if len(d.State.Outbox) != 0 {
			t.Fatal("alert before 120 seconds")
		}
	}
	// An index change must not silently move the watch to another physical GPU.
	gpu.GPUs[0].Index = 1
	d.sample(start.Add(120 * time.Second))
	d.sample(start.Add(150 * time.Second))
	if len(d.State.Watches) != 0 || len(d.State.Outbox) != 1 {
		t.Fatalf("watch did not complete exactly once: %+v", d.State)
	}
	if !strings.Contains(d.State.Outbox[0].Text, start.Add(120*time.Second).UTC().Format(time.RFC3339)) {
		t.Fatal("availability alert omitted observation time")
	}
}

func TestBusyAndUnknownResetConfirmation(t *testing.T) {
	cases := []string{"process", "memory", "load", "metric error", "device missing", "snapshot error", "sample gap"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			d, gpu := testDaemon(t)
			armTestWatch(d)
			start := time.Unix(1000, 0)
			d.sample(start)
			applyFault(name, gpu)
			when := start.Add(30 * time.Second)
			if name == "sample gap" {
				when = start.Add(121 * time.Second)
			}
			d.sample(when)
			if len(d.State.Outbox) != 0 {
				t.Fatal("fault produced an idle alert")
			}
			gpu.GPUs = []GPU{{UUID: "GPU-first"}}
			gpu.Err = nil
			d.sample(when.Add(30 * time.Second))
			if len(d.State.Outbox) != 0 || d.State.Watches[0].IdleSince.Before(when) {
				t.Fatal("confirmation window survived a fault")
			}
		})
	}
}

func applyFault(name string, gpu *fakeGPU) {
	switch name {
	case "process":
		gpu.GPUs[0].ComputeCount = 1
	case "memory":
		gpu.GPUs[0].MemoryUsed = 513 << 20
	case "load":
		gpu.GPUs[0].Utilization = 6
	case "metric error":
		gpu.GPUs[0].Error = "Not Supported"
	case "device missing":
		gpu.GPUs = nil
	case "snapshot error":
		gpu.Err = errors.New("driver lost")
	}
}

func TestRestartResetsDwellAndPreservesOutbox(t *testing.T) {
	d, _ := testDaemon(t)
	armTestWatch(d)
	d.sample(time.Now())
	d.State.queue("pending", "alert")
	if err := d.Store.Save(d.State); err != nil {
		t.Fatal(err)
	}
	loaded, err := d.Store.Load(d.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Watches[0].IdleSince.IsZero() || len(loaded.Outbox) != 1 {
		t.Fatal("restart persistence contract violated")
	}
	other := d.Config
	other.Server = "dencun"
	if _, err := d.Store.Load(other); err == nil {
		t.Fatal("accepted another server's state")
	}
}

func TestWatchExpiry(t *testing.T) {
	d, _ := testDaemon(t)
	armTestWatch(d)
	now := time.Now()
	d.State.Watches[0].Expires = now
	d.sample(now)
	if len(d.State.Watches) != 0 || len(d.State.Outbox) != 1 || d.State.Outbox[0].ID != "expired-watch1" {
		t.Fatal("expired watch was not retired")
	}
}

func TestExclusiveDaemonLock(t *testing.T) {
	d, _ := testDaemon(t)
	if err := d.Store.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Store.Close)
	other, err := NewStore(filepath.Clean(d.Config.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Lock(); err == nil {
		other.Close()
		t.Fatal("two daemons obtained the same lock")
	}
}
