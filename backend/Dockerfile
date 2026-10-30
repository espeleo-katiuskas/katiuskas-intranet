FROM golang:1.26-alpine AS build
WORKDIR /src
# The build context is the repository root (go.mod lives there, so that Go can
# stamp the module version from git), not ./backend.
COPY go.mod go.sum ./
RUN go mod download
COPY backend ./backend
RUN CGO_ENABLED=0 go build -o /out/api ./backend/cmd/api

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 64711
ENTRYPOINT ["api"]
