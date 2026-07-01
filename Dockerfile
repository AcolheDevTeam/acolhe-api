# syntax=docker/dockerfile:1

# ---------- build dos binários (api + worker) ----------
FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# ---------- CLI do Atlas (binário estático) p/ aplicar migrations no deploy ----------
FROM alpine:3.20 AS atlas
RUN apk add --no-cache curl \
 && curl -fsSL https://release.ariga.io/atlas/atlas-linux-amd64-latest -o /usr/local/bin/atlas \
 && chmod +x /usr/local/bin/atlas

# ---------- imagem final ----------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/api /out/worker /usr/local/bin/
COPY --from=atlas /usr/local/bin/atlas /usr/local/bin/atlas
# migrations + atlas.sum embutidos p/ o serviço `migrate` do compose
COPY internal/db/migrations /migrations
# roda como usuário não-root (Trivy DS-0002 / boa prática de container)
RUN addgroup -S app && adduser -S -G app app
USER app
EXPOSE 8080
# entrypoint padrão = API. O compose troca p/ `worker` ou `atlas` conforme o serviço.
ENTRYPOINT ["api"]
