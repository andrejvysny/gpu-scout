package scout

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type command struct {
	Action  string
	GPU     string
	WatchID string
	Expiry  time.Duration
}

func parseCommand(text, server string) (command, bool, error) {
	parts := strings.Fields(text)
	if len(parts) < 3 || parts[0] != "scout" || parts[2] != server {
		return command{}, false, nil
	}
	c := command{Action: parts[1]}
	options := map[string]string{}
	for _, part := range parts[3:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok || value == "" || options[key] != "" {
			return c, true, fmt.Errorf("invalid or repeated option: %s", part)
		}
		options[key] = value
	}
	switch c.Action {
	case "arm":
		c.GPU = options["gpu"]
		if c.GPU == "" {
			return c, true, fmt.Errorf("GPU required: scout arm %s gpu=0", server)
		}
		if value := options["expires"]; value != "" {
			var err error
			c.Expiry, err = time.ParseDuration(value)
			if err != nil || c.Expiry <= 0 || c.Expiry > 365*24*time.Hour {
				return c, true, fmt.Errorf("expires must be a positive duration up to 8760h")
			}
		}
		delete(options, "gpu")
		delete(options, "expires")
	case "cancel":
		c.WatchID = options["watch"]
		if c.WatchID == "" {
			return c, true, fmt.Errorf("watch ID required")
		}
		delete(options, "watch")
	case "status", "watches", "help":
	default:
		return c, true, fmt.Errorf("unknown command; use scout help %s", server)
	}
	if len(options) != 0 {
		return c, true, fmt.Errorf("unknown option")
	}
	return c, true, nil
}

func (d *Daemon) execute(c command, user, id string, now time.Time) Reply {
	switch c.Action {
	case "status":
		return d.status()
	case "arm":
		return d.arm(c, user, id, now)
	case "watches":
		return d.watches(user)
	case "cancel":
		return d.cancel(c.WatchID, user)
	default:
		return helpReply(user)
	}
}

func (d *Daemon) arm(c command, user, id string, now time.Time) Reply {
	gpus, err := d.GPUs.Snapshot()
	if err != nil {
		return infoReply("Cannot arm monitor", escape(err.Error()))
	}
	for _, g := range gpus {
		if c.GPU != strconv.Itoa(g.Index) && c.GPU != g.UUID {
			continue
		}
		if g.Error != "" {
			return infoReply("GPU metrics unavailable", escape(g.Error))
		}
		for _, w := range d.State.Watches {
			if w.User == user && w.UUID == g.UUID && (w.Expires.IsZero() || now.Before(w.Expires)) {
				return watchReply("Monitor already armed", w, d.Config)
			}
		}
		w := Watch{ID: id, UUID: g.UUID, User: user, GPUName: g.Name, GPUIndex: g.Index}
		if c.Expiry > 0 {
			w.Expires = now.Add(c.Expiry)
		}
		d.State.Watches = append(d.State.Watches, w)
		return watchReply("Monitor armed", w, d.Config)
	}
	return infoReply("GPU not found", "Use `/scout status "+d.Config.Server+"` to see GPU indexes and UUIDs.")
}

func (d *Daemon) status() Reply {
	gpus, err := d.GPUs.Snapshot()
	if err != nil {
		return infoReply(d.Config.Server+" · GPU state unknown", escape(err.Error()))
	}
	observed := time.Now()
	lines := []string{d.Config.Server + " at " + observed.UTC().Format(time.RFC3339)}
	blocks := []Block{header(d.Config.Server + " · GPU status")}
	for _, g := range gpus {
		lines = append(lines, g.summary())
		if len(blocks) < 44 {
			blocks = append(blocks, gpuBlocks(g, d.Config)...)
			blocks = append(blocks, Block{Type: "divider"})
		}
	}
	if len(gpus) == 0 {
		lines = append(lines, "No GPUs detected.")
		blocks = append(blocks, section("No GPUs detected."))
	}
	blocks = append(blocks, contextBlock("Observed "+formatTime(observed)+". Snapshot only; bookings are not checked."))
	return Reply{Text: strings.Join(lines, "\n"), Blocks: blocks}
}

func (d *Daemon) watches(user string) Reply {
	lines := []string{d.Config.Server + ": your watches"}
	blocks := []Block{header(d.Config.Server + " · Your watches")}
	for _, w := range d.State.Watches {
		if w.User != user {
			continue
		}
		expiry := "no expiry"
		if !w.Expires.IsZero() {
			expiry = "expires " + w.Expires.UTC().Format(time.RFC3339)
		}
		lines = append(lines, fmt.Sprintf("%s: %s, %s", w.ID, w.UUID, expiry))
		if len(blocks) < 44 {
			blocks = append(blocks, section("*Watch `"+w.ID+"`*", field("GPU", watchLabel(w)), field("Expiry", expiry)), contextBlock("UUID: `"+w.UUID+"`"))
		}
	}
	if len(lines) == 1 {
		lines = append(lines, "None.")
		blocks = append(blocks, section("No active watches. Create one with `/scout arm "+d.Config.Server+" gpu=0`."))
	}
	return Reply{Text: strings.Join(lines, "\n"), Blocks: blocks}
}

func (d *Daemon) cancel(id, user string) Reply {
	for i, w := range d.State.Watches {
		if w.ID == id && w.User == user {
			d.State.Watches = append(d.State.Watches[:i], d.State.Watches[i+1:]...)
			return infoReply("Monitor cancelled", d.Config.Server+" · Watch `"+id+"`")
		}
	}
	return infoReply("Cannot cancel monitor", "Watch not found or belongs to another user.")
}
