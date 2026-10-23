.PHONY: test build linux

test:
	go test -race ./...
	go vet ./...

build:
	go build -trimpath -ldflags="-s -w" -o dist/gpu-scout ./cmd/gpu-scout

linux:
	docker buildx build --platform linux/amd64 --output type=local,dest=dist .
	COPYFILE_DISABLE=1 tar --no-xattrs -czf dist/gpu-scout-linux-amd64.tar.gz -C dist gpu-scout SHA256SUMS licenses -C .. README.md config.example.json deploy scripts/install.sh
