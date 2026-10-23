//go:build !linux || !cgo

package scout

import "errors"

func OpenGPU() (GPUReader, error) {
	return nil, errors.New("GPU monitoring requires a Linux build with CGO enabled and the NVIDIA driver installed")
}
