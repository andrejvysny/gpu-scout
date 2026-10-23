package scout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type RunEvent struct {
	ID       string        `json:"id"`
	Server   string        `json:"server"`
	User     string        `json:"user"`
	Label    string        `json:"label"`
	ExitCode int           `json:"exit_code"`
	Elapsed  time.Duration `json:"elapsed"`
	Finished time.Time     `json:"finished"`
}

func Run(c Config, label, user string, args []string) (int, error) {
	if !userPattern.MatchString(user) || len(args) == 0 || len([]rune(label)) > 200 || strings.ContainsAny(label, "\r\n") {
		return 2, errors.New("run requires --notify-user Slack ID, a label up to 200 characters, and -- <command>")
	}
	store, err := NewStore(c.StateDir)
	if err != nil {
		return 2, err
	}
	id, err := newID()
	if err != nil {
		return 2, err
	}
	start := time.Now()
	code, runErr := executeRun(args)
	event := RunEvent{ID: id, Server: c.Server, User: user, Label: label, ExitCode: code, Elapsed: time.Since(start), Finished: time.Now().UTC()}
	if err := writeJSON(filepath.Join(store.Dir, "runs", id+".json"), event); err != nil {
		return code, fmt.Errorf("command exited %d; completion could not be queued: %w", code, err)
	}
	return code, runErr
}

func executeRun(args []string) (int, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return 127, err
	}
	done := make(chan struct{})
	defer close(done)
	go forwardSignals(cmd, signals, done)
	err := cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), nil
		}
		return exitErr.ExitCode(), nil
	}
	return 1, err
}

func forwardSignals(cmd *exec.Cmd, signals <-chan os.Signal, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case sig := <-signals:
			if unixSignal, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(-cmd.Process.Pid, unixSignal)
			}
		}
	}
}

func (d *Daemon) ingestRuns() error {
	entries, err := os.ReadDir(filepath.Join(d.Store.Dir, "runs"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := d.ingestRun(entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) ingestRun(name string) error {
	path := filepath.Join(d.Store.Dir, "runs", name)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var event RunEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("invalid run event %s: %w", name, err)
	}
	if event.ID+".json" != name || event.Server != d.Config.Server || !userPattern.MatchString(event.User) {
		return fmt.Errorf("invalid run event identity: %s", name)
	}
	if !d.State.SeenRuns[event.ID] {
		d.State.queueReply("run-"+event.ID, runReply(event))
		d.State.SeenRuns[event.ID] = true
		if err := d.Store.Save(d.State); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	delete(d.State.SeenRuns, event.ID)
	return d.Store.Save(d.State)
}
