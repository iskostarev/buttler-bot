buttler-bot: go.mod go.sum *.go
	CGO_ENABLED=1 CC=musl-gcc go build -tags netgo,goolm --ldflags '-linkmode external -extldflags=-static'
