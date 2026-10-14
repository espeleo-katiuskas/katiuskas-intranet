FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION is optional (docker build --build-arg VERSION=v1.2.3): the .git
# directory isn't in the build context, so the image can't work it out itself.
ARG VERSION=""
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/api ./cmd/api

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 64711
ENTRYPOINT ["api"]
