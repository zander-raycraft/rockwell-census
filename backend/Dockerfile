FROM golang:1.23.12 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY src ./src
RUN CGO_ENABLED=0 go build -o /out/census ./src

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=build /out/census /app/census
COPY migrations /app/migrations

ENTRYPOINT ["/app/census"]
