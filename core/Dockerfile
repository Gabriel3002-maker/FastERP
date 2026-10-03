# Debe coincidir con la directiva `go` de go.mod. Con una imagen menor,
# `go mod download` falla con "go.mod requires go >= 1.25.0".
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata curl

# Corre sin privilegios: si el proceso se ve comprometido, no es root dentro del contenedor.
RUN adduser -D -u 10001 fasterp
USER fasterp

COPY --from=builder /server /server

# El volumen de subidas lo monta docker-compose. Docker lo crea como root en el
# primer arranque y el proceso corre como 10001, así que el directorio tiene que
# ser de ese uid ANTES de que se cree el volumen: si no, MkdirAll falla y toda
# subida se cae silenciosamente.
RUN mkdir -p /app/uploads && chown 10001:10001 /app/uploads

EXPOSE 7071
CMD ["/server"]
