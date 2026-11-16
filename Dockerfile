FROM golang:1.27.4 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /manager ./cmd

FROM scratch
COPY --from=builder /manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
