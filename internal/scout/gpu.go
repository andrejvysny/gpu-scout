package scout

import (
	"fmt"
	"strings"
)

type GPU struct {
	Index        int
	UUID         string
	Name         string
	MemoryUsed   uint64
	MemoryTotal  uint64
	Utilization  uint32
	ComputeCount int
	Error        string
}

type GPUReader interface {
	Snapshot() ([]GPU, error)
	Close()
}

func (g GPU) unused(c Config) bool {
	return g.Error == "" && g.ComputeCount == 0 && g.Utilization <= c.MaxUtilization && g.MemoryUsed <= c.MaxMemoryMiB*1024*1024
}

func (g GPU) summary() string {
	if g.Error != "" {
		return fmt.Sprintf("GPU %d %s (%s): unknown: %s", g.Index, escape(g.Name), g.UUID, escape(g.Error))
	}
	return fmt.Sprintf("GPU %d %s (%s): %d%% load, %d/%d MiB, %d compute processes", g.Index, escape(g.Name), g.UUID, g.Utilization, g.MemoryUsed/(1024*1024), g.MemoryTotal/(1024*1024), g.ComputeCount)
}

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
