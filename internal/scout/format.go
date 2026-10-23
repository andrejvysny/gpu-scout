package scout

import (
	"fmt"
	"strings"
	"time"
)

type TextObject struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Block struct {
	Type     string       `json:"type"`
	Text     *TextObject  `json:"text,omitempty"`
	Fields   []TextObject `json:"fields,omitempty"`
	Elements []TextObject `json:"elements,omitempty"`
}

type Reply struct {
	Text   string
	Blocks []Block
}

func header(text string) Block {
	return Block{Type: "header", Text: &TextObject{Type: "plain_text", Text: text}}
}

func section(text string, fields ...TextObject) Block {
	return Block{Type: "section", Text: &TextObject{Type: "mrkdwn", Text: text}, Fields: fields}
}

func field(label, value string) TextObject {
	return TextObject{Type: "mrkdwn", Text: "*" + label + "*\n" + value}
}

func contextBlock(text string) Block {
	return Block{Type: "context", Elements: []TextObject{{Type: "mrkdwn", Text: text}}}
}

func infoReply(title, text string) Reply {
	return Reply{Text: title + ": " + text, Blocks: []Block{header(title), section(text)}}
}

func (r Reply) forUser(user string) Reply {
	r.Text = fmt.Sprintf("<@%s> %s", user, r.Text)
	r.Blocks = append(r.Blocks, contextBlock("Requested by <@"+user+">"))
	return r
}

func formatTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") }

func gpuBlocks(g GPU, c Config) []Block {
	state := "In use"
	if g.unused(c) {
		state = "Idle now"
	}
	if g.Error != "" {
		return []Block{section(fmt.Sprintf("*GPU %d · %s*\nMetrics unavailable: %s", g.Index, escape(g.Name), escape(g.Error))), contextBlock("UUID: `" + g.UUID + "`")}
	}
	return []Block{
		section(fmt.Sprintf("*GPU %d · %s*", g.Index, escape(strings.TrimPrefix(g.Name, "NVIDIA "))),
			field("State", state), field("Load", fmt.Sprintf("%d%%", g.Utilization)),
			field("Memory", fmt.Sprintf("%.1f / %.1f GiB", float64(g.MemoryUsed)/(1<<30), float64(g.MemoryTotal)/(1<<30))),
			field("Compute processes", fmt.Sprint(g.ComputeCount))),
		contextBlock("UUID: `" + g.UUID + "`"),
	}
}

func watchReply(title string, w Watch, c Config) Reply {
	expiry := "No expiry"
	if !w.Expires.IsZero() {
		expiry = formatTime(w.Expires)
	}
	gpu := watchLabel(w)
	text := fmt.Sprintf("%s: %s, GPU %s, watch=%s; idle confirmation %s; %s", title, c.Server, gpu, w.ID, c.IdlePeriod, expiry)
	return Reply{Text: text, Blocks: []Block{header(title), section("Notify once this GPU stays unused.",
		field("Server", c.Server), field("GPU", gpu), field("Idle confirmation", duration(c.IdlePeriod).String()), field("Expiry", expiry)),
		contextBlock("Watch ID: `" + w.ID + "` · GPU UUID: `" + w.UUID + "`"),
		contextBlock("Bookings are not checked. Use `/scout cancel " + c.Server + " watch=" + w.ID + "` to cancel.")}}
}

func watchLabel(w Watch) string {
	if w.GPUName == "" {
		return w.UUID
	}
	return fmt.Sprintf("%d · %s", w.GPUIndex, escape(strings.TrimPrefix(w.GPUName, "NVIDIA ")))
}

func expiredReply(w Watch, c Config) Reply {
	return infoReply(c.Server+" · Watch expired", "Watch `"+w.ID+"` expired at "+formatTime(w.Expires)+" before idle confirmation. Arm a new watch if still needed.").forUser(w.User)
}

func helpReply(user string) Reply {
	commands := "```/scout status dencun\n/scout arm dencun gpu=0 expires=10m\n/scout watches dencun\n/scout cancel dencun watch=WATCH_ID\n/scout help```"
	text := "GPU Scout help\n" + commands + "\nUse dencun or pectra. GPU selection is required. Alerts do not grant booking permission."
	run := "gpu-scout run --label experiment --notify-user " + user + " -- python train.py"
	return Reply{Text: text, Blocks: []Block{header("GPU Scout · Help"),
		section("*Check status and manage watches*\n" + commands),
		section("*Choose a GPU*\nUse `gpu=0`, `gpu=1`, or a full UUID from status. Replace `dencun` with `pectra` for that server."),
		section("*Watch behavior*\nOne alert after no compute processes, low load, and low memory use for the configured confirmation period (default: 2 minutes). `expires=` is optional; examples: `30m`, `24h`. Otherwise the watch has no expiry. You can list or cancel only your own watches."),
		section("*Training finished?*\nRun this over SSH under the service owner's Linux account:\n```" + run + "```\nReports command exit, including failure. Slack never launches training jobs."),
		contextBlock("Plain channel messages also work: `scout status dencun` or `scout help dencun`. Bookings must be checked separately.")}}
}

func idleReply(w Watch, g GPU, c Config, now time.Time) Reply {
	text := fmt.Sprintf("<@%s> %s GPU %d (%s) observed unused for %s at %s. Check current status and booking before use. Watch %s completed.", w.User, c.Server, g.Index, g.UUID, c.IdlePeriod, now.UTC().Format(time.RFC3339), w.ID)
	blocks := []Block{header(c.Server + " · GPU idle"), section("<@" + w.User + "> Your GPU watch completed.")}
	blocks = append(blocks, gpuBlocks(g, c)...)
	blocks = append(blocks, contextBlock("Observed "+formatTime(now)+" · Confirmed for "+c.IdlePeriod+" · Watch `"+w.ID+"`"),
		contextBlock("Check current status and booking before use. This is an observation, not a reservation."))
	return Reply{Text: text, Blocks: blocks}
}

func runReply(event RunEvent) Reply {
	result := "Succeeded"
	if event.ExitCode != 0 {
		result = "Failed"
	}
	text := fmt.Sprintf("<@%s> %s: %s %s (exit %d, %s). Finished %s. GPU availability is checked separately.", event.User, event.Server, escape(event.Label), strings.ToLower(result), event.ExitCode, event.Elapsed.Round(time.Second), event.Finished.UTC().Format(time.RFC3339))
	return Reply{Text: text, Blocks: []Block{header(event.Server + " · Training finished"),
		section("<@"+event.User+"> *"+escape(event.Label)+"*", field("Result", result), field("Exit code", fmt.Sprint(event.ExitCode)), field("Duration", event.Elapsed.Round(time.Second).String()), field("Finished", formatTime(event.Finished))),
		contextBlock("GPU availability is checked separately. Run ID: `" + event.ID + "`")}}
}
