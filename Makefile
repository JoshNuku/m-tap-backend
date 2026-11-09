.PHONY: build run seed test docker-up docker-down tidy clean

# Default binary name
BIN_DIR=bin
SERVER_BIN=$(BIN_DIR)/m-tap-server
SEED_BIN=$(BIN_DIR)/m-tap-seed
BENCH_BIN=$(BIN_DIR)/m-tap-bench

tidy:
	go mod tidy

build: tidy
	mkdir -p $(BIN_DIR)
	go build -o $(SERVER_BIN) cmd/server/main.go
	go build -o $(SEED_BIN) cmd/seed/main.go
	go build -o $(BENCH_BIN) cmd/simulator/main.go

run: build
	./$(SERVER_BIN)

seed: build
	./$(SEED_BIN)

bench: build
	./$(BENCH_BIN) -n 50 -c 5

test:
	go test -v -race ./...

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down -v

clean:
	rm -rf $(BIN_DIR)

