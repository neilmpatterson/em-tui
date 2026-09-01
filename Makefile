BIN := bin/em-tui

.PHONY: build run test clean

build:
	go build -o $(BIN) ./cmd/em-tui

run: build
	./$(BIN)

test:
	go test ./...

clean:
	rm -f $(BIN)
