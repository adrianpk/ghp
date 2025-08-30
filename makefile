# Default GitHub user to profile if GHUSER is not provided as an argument.
GHUSER ?= default_github_user

run-openai:
	OPENAI_API_KEY=$$OPENAI_API_KEY go run ./main.go --config ./config/config.yml --user $(GHUSER) --provider openai

run-gemini:
	GEMINI_API_KEY=$$GEMINI_API_KEY go run ./main.go --config ./config/config.yml --user $(GHUSER) --provider gemini

build:
	go build -o bin/ghp ./main.go

