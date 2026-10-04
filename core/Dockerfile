# Debe coincidir con la directiva `go` de go.mod. Con una imagen menor,
# `go mod download` falla con "go.mod requires go >= 1.26.0".
FROM golang:1.26-alpine AS builder

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

# La UI es parte del producto, no un extra: sin estos dos directorios Gin sirve
# "/" y todo lo de /admin sale como error de plantilla en cada petición. Se
# copian desde el builder (el contexto de build es ./core) en vez de montarlos
# como volúmenes para que la imagen sea autosuficiente.
COPY --from=builder --chown=10001:10001 /app/static /app/static
COPY --from=builder --chown=10001:10001 /app/templates /app/templates

# El volumen de subidas lo monta docker-compose. Docker lo crea como root en el
# primer arranque y el proceso corre como 10001, así que el directorio tiene que
# ser de ese uid ANTES de que se cree el volumen: si no, MkdirAll falla y toda
# subida se cae silenciosamente.
RUN mkdir -p /app/uploads && chown 10001:10001 /app/uploads

ENV FASTERP_STATIC_DIR=/app/static \
    FASTERP_TEMPLATES_DIR=/app/templates \
    FASTERP_UPLOAD_DIR=/app/uploads \
    FASTERP_MODULES_DIR=/app/modules

EXPOSE 7071
CMD ["/server"]
