//go:build linux && cgo

package scout

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type nvmlReader struct{}

func OpenGPU() (GPUReader, error) {
	if result := nvml.Init(); result != nvml.SUCCESS {
		return nil, fmt.Errorf("NVML initialization: %s", nvml.ErrorString(result))
	}
	return &nvmlReader{}, nil
}

func (r *nvmlReader) Close() { nvml.Shutdown() }

func (r *nvmlReader) Snapshot() ([]GPU, error) {
	count, result := nvml.DeviceGetCount()
	if result != nvml.SUCCESS {
		return nil, fmt.Errorf("NVML device count: %s", nvml.ErrorString(result))
	}
	gpus := make([]GPU, 0, count)
	for i := 0; i < count; i++ {
		device, result := nvml.DeviceGetHandleByIndex(i)
		if result != nvml.SUCCESS {
			return nil, fmt.Errorf("NVML GPU %d handle: %s", i, nvml.ErrorString(result))
		}
		gpu, err := readDevice(device, i)
		if err != nil {
			return nil, err
		}
		gpus = append(gpus, gpu)
	}
	return gpus, nil
}

func readDevice(device nvml.Device, index int) (GPU, error) {
	g := GPU{Index: index}
	uuid, result := device.GetUUID()
	if result != nvml.SUCCESS {
		return g, fmt.Errorf("GPU %d UUID: %s", index, nvml.ErrorString(result))
	}
	g.UUID = uuid
	g.Name, result = device.GetName()
	if result != nvml.SUCCESS {
		g.Name = "NVIDIA GPU"
	}
	if mode, _, result := device.GetMigMode(); result == nvml.SUCCESS && mode != 0 {
		g.Error = "MIG devices are not supported in v1"
		return g, nil
	}
	readMetrics(device, &g)
	return g, nil
}

func readMetrics(device nvml.Device, g *GPU) {
	memory, result := device.GetMemoryInfo()
	if result != nvml.SUCCESS {
		g.Error = "memory: " + nvml.ErrorString(result)
		return
	}
	g.MemoryUsed, g.MemoryTotal = memory.Used, memory.Total
	load, result := device.GetUtilizationRates()
	if result != nvml.SUCCESS {
		g.Error = "utilization: " + nvml.ErrorString(result)
		return
	}
	g.Utilization = load.Gpu
	processes, result := device.GetComputeRunningProcesses()
	if result != nvml.SUCCESS {
		g.Error = "compute processes: " + nvml.ErrorString(result)
		return
	}
	g.ComputeCount = len(processes)
}
