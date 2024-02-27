buttler-bot: go.mod go.sum *.go
	go build -tags netgo,goolm

govulncheck:
	govulncheck .

analyze:
	~/go/bin/staticcheck .
	~/go/bin/nilaway --include-pkgs=main .

.PHONY: govulncheck analyze
