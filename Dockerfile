# сборка. обычный (не alpine) образ - в нем есть gcc, нужен для go test -race
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/server /usr/local/bin/server
EXPOSE 8080
USER nobody
ENTRYPOINT ["server"]
