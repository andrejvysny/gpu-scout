package scout

import (
	"log/slog"
	"time"
)

func (d *Daemon) sample(now time.Time) {
	if len(d.State.Watches) == 0 {
		d.lastSample = time.Time{}
		return
	}
	gpus, err := d.GPUs.Snapshot()
	byUUID := make(map[string]GPU, len(gpus))
	for _, g := range gpus {
		byUUID[g.UUID] = g
	}
	if err != nil {
		slog.Warn("GPU sample failed", "error", err)
	}
	gap := !d.lastSample.IsZero() && now.Sub(d.lastSample) > 2*duration(d.Config.GPUPoll)
	watches := d.State.Watches[:0]
	for _, w := range d.State.Watches {
		if !w.Expires.IsZero() && !now.Before(w.Expires) {
			d.State.queueReply("expired-"+w.ID, expiredReply(w, d.Config))
			continue
		}
		g, found := byUUID[w.UUID]
		if gap || err != nil || !found || !g.unused(d.Config) {
			w.IdleSince = time.Time{}
		}
		if err == nil && found && g.unused(d.Config) {
			if w.IdleSince.IsZero() {
				w.IdleSince = now
			}
			if now.Sub(w.IdleSince) >= duration(d.Config.IdlePeriod) {
				d.State.queueReply("idle-"+w.ID, idleReply(w, g, d.Config, now))
				continue
			}
		}
		watches = append(watches, w)
	}
	d.State.Watches = watches
	d.lastSample = now
}
