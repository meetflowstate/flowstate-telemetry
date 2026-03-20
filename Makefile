.PHONY: build test clean install

build:
	go build -o bin/flowstate-telemetry .

test:
	go test ./...

clean:
	rm -rf bin/

install: build
	cp bin/flowstate-telemetry /usr/local/bin/
