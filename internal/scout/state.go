package scout

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Watch struct {
	ID        string    `json:"id"`
	User      string    `json:"user"`
	UUID      string    `json:"uuid"`
	GPUName   string    `json:"gpu_name,omitempty"`
	GPUIndex  int       `json:"gpu_index,omitempty"`
	Expires   time.Time `json:"expires,omitempty"`
	IdleSince time.Time `json:"-"`
}

type Notification struct {
	ID       string           `json:"id"`
	Text     string           `json:"text"`
	Blocks   []Block          `json:"blocks,omitempty"`
	Metadata *CommandMetadata `json:"metadata,omitempty"`
	Attempts int              `json:"attempts"`
	NextTry  time.Time        `json:"next_try"`
}

type State struct {
	Server       string               `json:"server"`
	Channel      string               `json:"channel"`
	Cursor       string               `json:"cursor"`
	Watches      []Watch              `json:"watches"`
	Outbox       []Notification       `json:"outbox"`
	SeenRuns     map[string]bool      `json:"seen_runs"`
	SeenCommands map[string]time.Time `json:"seen_commands,omitempty"`
}

type Store struct {
	Dir  string
	lock *os.File
}

func NewStore(dir string) (*Store, error) {
	for _, path := range []string{dir, filepath.Join(dir, "runs"), filepath.Join(dir, "requests")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
		if err := os.Chmod(path, 0700); err != nil {
			return nil, err
		}
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) Lock() error {
	f, err := os.OpenFile(filepath.Join(s.Dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return fmt.Errorf("another daemon is using this state directory: %w", err)
	}
	s.lock = f
	return nil
}

func (s *Store) Close() {
	if s.lock != nil {
		s.lock.Close()
	}
}

func (s *Store) Load(c Config) (State, error) {
	state := State{Server: c.Server, Channel: c.SlackChannel, SeenRuns: make(map[string]bool)}
	data, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("invalid state; refusing to discard watches: %w", err)
	}
	if state.Server != c.Server || state.Channel != c.SlackChannel {
		return state, errors.New("state belongs to another server/channel; use a separate state_dir")
	}
	if state.SeenRuns == nil {
		state.SeenRuns = make(map[string]bool)
	}
	return state, nil
}

func (s *Store) Save(state State) error {
	return writeJSON(filepath.Join(s.Dir, "state.json"), state)
}

func writeJSON(path string, value interface{}) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = json.NewEncoder(f).Encode(value); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (s *State) queue(id, text string) {
	s.Outbox = append(s.Outbox, Notification{ID: id, Text: text})
}

func (s *State) queueReply(id string, r Reply) {
	s.Outbox = append(s.Outbox, Notification{ID: id, Text: r.Text, Blocks: r.Blocks})
}

func (s *State) remember(id string, now time.Time) {
	if s.SeenCommands == nil {
		s.SeenCommands = make(map[string]time.Time)
	}
	s.SeenCommands[id] = now
}
