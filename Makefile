.PHONY: migrate-up migrate-down run build

migrate-up:
	goose -dir db/migrations postgres "$$DATABASE_URL" up

migrate-down:
	goose -dir db/migrations postgres "$$DATABASE_URL" down

run:
	cd backend && go run ./cmd/api

build:
	cd backend && go build -o ../bin/api ./cmd/api
