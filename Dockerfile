FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ssh-proxy ./cmd/ssh-proxy/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /ssh-proxy /usr/local/bin/ssh-proxy
EXPOSE 2022
ENTRYPOINT ["ssh-proxy"]
