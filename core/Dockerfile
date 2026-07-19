# Debe coincidir con la directiva `go` de go.mod (1.25). Con una imagen menor,
# `go mod download` falla con "go.mod requires go >= 1.25.0".
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata

# Corre sin privilegios: si el proceso se ve comprometido, no es root dentro del contenedor.
RUN adduser -D -u 10001 fasterp
USER fasterp

COPY --from=builder /server /server
EXPOSE 7071
CMD ["/server"]
