FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Builds both ssh-proxy and the cleanlog helper into /out.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ /usr/local/bin/
EXPOSE 2022
ENTRYPOINT ["ssh-proxy"]
