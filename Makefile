setup:
	go get github.com/stretchr/testify

test:
	echo "Running tests"
	go test -race ./...

compile:
	echo "Compiling for every OS and Platform"
	GOOS=darwin GOARCH=amd64 go build -o bin/notifier-macos cmd/main.go
	GOOS=linux GOARCH=amd64 go build -o bin/notifier-linux cmd/main.go
	GOOS=windows GOARCH=amd64 go build -o bin/notifier-windows.exe cmd/main.go

all: setup test compile