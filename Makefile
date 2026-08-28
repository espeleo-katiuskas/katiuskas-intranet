.PHONY: migrate-up migrate-down seed-admin run build

migrate-up:
	goose -dir db/migrations postgres "$$DATABASE_URL" up

migrate-down:
	goose -dir db/migrations postgres "$$DATABASE_URL" down

seed-admin:
	cd backend && go run ./cmd/seed-admin

run:
	cd backend && go run ./cmd/api

build:
	cd backend && go build -o ../bin/api ./cmd/api
