FROM golang:1.26.0-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ENV CGO_ENABLED=1
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go test -race ./... && go vet ./...
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/gpu-scout ./cmd/gpu-scout && \
    mkdir -p /out/licenses/go-nvml && \
    nvml_dir=$(go list -m -f '{{.Dir}}' github.com/NVIDIA/go-nvml) && \
    cp "$nvml_dir/LICENSE" /out/licenses/go-nvml/LICENSE && \
    cp "$nvml_dir/THIRD_PARTY_NOTICES.md" /out/licenses/go-nvml/THIRD_PARTY_NOTICES.md && \
    mkdir -p /out/licenses/websocket && \
    websocket_dir=$(go list -m -f '{{.Dir}}' github.com/coder/websocket) && \
    cp "$websocket_dir/LICENSE.txt" /out/licenses/websocket/LICENSE.txt
RUN cd /out && sha256sum gpu-scout > SHA256SUMS

FROM scratch AS artifact
COPY --from=build /out/ /
