# локальные команды. для запуска нужен только docker, make - для удобства
TEST_DB ?= postgres://prices:prices@localhost:5433/prices?sslmode=disable

.PHONY: up down logs test test-unit test-docker fmt vet demo two

up:
	docker compose up --build -d
	@echo "http://localhost:8080/healthz"

down:
	docker compose down

logs:
	docker compose logs -f app

# тесты локально (нужен go), база из docker compose
test:
	docker compose up -d db
	TEST_DATABASE_URL="$(TEST_DB)" go test -race -count=1 ./...

# только то что без базы
test-unit:
	go test ./...

# вообще все в докере, go ставить не надо
test-docker:
	docker compose --profile test run --rm --build tests

fmt:
	gofmt -w .

vet:
	go vet ./...

# демо с нуля. ВНИМАНИЕ: сносит базу (down -v)
demo:
	docker compose down -v
	docker compose up -d --build --wait
	./scripts/demo.sh

# гонка apply через два экземпляра сервиса
two:
	./scripts/two_instances.sh
