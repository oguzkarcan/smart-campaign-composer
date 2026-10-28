# Builds every binary under cmd/ into one image; compose picks one per service.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/ /app/
USER 65534:65534
