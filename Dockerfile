# syntax=docker/dockerfile:1

FROM golang:1.22 AS build
WORKDIR /app
COPY go.mod .
RUN go mod download
COPY ../../../Downloads/url-shortener-go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/bin/shortener main.go

FROM gcr.io/distroless/base-debian12
WORKDIR /
COPY --from=build /app/bin/shortener /shortener
EXPOSE 8080
ENTRYPOINT ["/shortener"]
