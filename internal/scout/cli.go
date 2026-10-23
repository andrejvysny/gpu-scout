package scout

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const Version = "0.2.0"

func CLI(args []string, out, stderr io.Writer) int {
	path, err := DefaultConfigPath()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	flags := flag.NewFlagSet("gpu-scout", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&path, "config", path, "configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	args = flags.Args()
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(out, "gpu-scout [-config FILE] serve|doctor|run|version\nrun: --label NAME --notify-user U123 -- COMMAND [ARG...]")
		return 0
	}
	if args[0] == "version" {
		fmt.Fprintln(out, Version)
		return 0
	}
	c, err := LoadConfig(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	code, err := dispatch(c, args, out, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	return code
}

func dispatch(c Config, args []string, out, stderr io.Writer) (int, error) {
	if args[0] == "run" {
		return runCLI(c, args[1:], stderr)
	}
	if len(args) != 1 || (args[0] != "doctor" && args[0] != "serve") {
		return 2, errors.New("expected serve, doctor, run, or version; use help")
	}
	token := os.Getenv("SLACK_BOT_TOKEN")
	if token == "" {
		return 1, errors.New("SLACK_BOT_TOKEN is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slack := NewSlack(token, c.SlackChannel)
	gpus, err := OpenGPU()
	if err != nil {
		return 1, err
	}
	defer gpus.Close()
	if args[0] == "doctor" {
		return doctor(ctx, c, slack, gpus, out)
	}
	err = serve(ctx, c, slack, gpus)
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func runCLI(c Config, args []string, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	label := flags.String("label", "training", "run label (no command arguments are sent to Slack)")
	user := flags.String("notify-user", "", "Slack user ID to mention")
	if err := flags.Parse(args); err != nil {
		return 2, err
	}
	return Run(c, *label, *user, flags.Args())
}

func serve(ctx context.Context, c Config, slack *Slack, gpus GPUReader) error {
	store, err := NewStore(c.StateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Lock(); err != nil {
		return err
	}
	state, err := store.Load(c)
	if err != nil {
		return err
	}
	d := Daemon{Config: c, State: state, Store: store, Slack: slack, GPUs: gpus}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if appToken := os.Getenv("SLACK_APP_TOKEN"); appToken != "" {
		receiver := SocketReceiver{Slack: slack, AppToken: appToken, Config: c, Store: store}
		done := make(chan struct{})
		go func() { defer close(done); receiver.Run(ctx) }()
		defer func() { cancel(); <-done }()
	}
	return d.Serve(ctx)
}

func doctor(ctx context.Context, c Config, slack *Slack, gpus GPUReader, out io.Writer) (int, error) {
	store, err := NewStore(c.StateDir)
	if err != nil {
		return 1, err
	}
	if _, err := store.Load(c); err != nil {
		return 1, err
	}
	if err := slack.Check(ctx); err != nil {
		return 1, err
	}
	if token := os.Getenv("SLACK_APP_TOKEN"); token != "" {
		receiver := SocketReceiver{Slack: slack, AppToken: token, Config: c, Store: store}
		if _, err := receiver.socketURL(ctx); err != nil {
			return 1, err
		}
		fmt.Fprintln(out, "Slack app-level token: Socket Mode connection permission OK")
	}
	snapshot, err := gpus.Snapshot()
	if err != nil {
		return 1, err
	}
	if len(snapshot) == 0 {
		return 1, errors.New("no NVIDIA GPUs detected")
	}
	fmt.Fprintln(out, c.Server+": state directory and Slack access OK; no Slack messages sent")
	for _, g := range snapshot {
		fmt.Fprintln(out, g.summary())
		if g.Error != "" {
			return 1, errors.New("required GPU metrics unavailable")
		}
	}
	return 0, nil
}
