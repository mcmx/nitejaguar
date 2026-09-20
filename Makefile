# Simple Makefile for a Go project

# Build the application
all: build test
# NOTE: the templ generator version must match the templ runtime version in
# go.mod. A newer generator emits code (e.g. templ.ResolveAttributeValue)
# that the pinned runtime does not have, which breaks `go build` and lint.
# Keep this in sync with go.mod and the "Install templ" steps in
# .github/workflows/*.yml.
TEMPL_VERSION := v0.3.1020
GOLANGCI_VERSION := v2.13.2
# Pinned for the same reason as TEMPL_VERSION: an unpinned `latest` binary
# can generate different CSS from identical sources on different machines.
TAILWIND_VERSION := v4.1.6

templ-install:
	@if ! command -v templ > /dev/null; then \
		read -p "Go's 'templ' is not installed on your machine. Do you want to install it? [Y/n] " choice; \
		if [ "$$choice" != "n" ] && [ "$$choice" != "N" ]; then \
			go install github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION); \
			if [ ! -x "$$(command -v templ)" ]; then \
				echo "templ installation failed. Exiting..."; \
				exit 1; \
			fi; \
		else \
			echo "You chose not to install templ. Exiting..."; \
			exit 1; \
		fi; \
	fi
golangci-install:
	@if ! command -v golangci-lint > /dev/null; then \
		echo "Installing golangci-lint $(GOLANGCI_VERSION) (matches CI)..."; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION); \
		if [ ! -x "$$(command -v golangci-lint)" ]; then \
			echo "golangci-lint installation failed. Exiting..."; \
			exit 1; \
		fi; \
	fi
tailwind:
	@if [ ! -f tailwindcss ]; then curl -sL https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-linux-x64 -o tailwindcss; fi
	
	@chmod +x tailwindcss

ent:
	@go generate ./ent

templ-watch:
	@templ generate --watch --proxy="http://localhost:8081" --open-browser=true -v

tailwind-watch:
	@./tailwindcss -i cmd/web/assets/css/input.css -o cmd/web/assets/css/output.css --watch

build: ent tailwind templ-install
	@echo "Building..."
	@templ generate
	@./tailwindcss -i cmd/web/assets/css/input.css -o cmd/web/assets/css/output.css
	@go build -o main main.go

# Run the application
run:
	@go run main.go server -e

# Test the application
# Mirrors .github/workflows/go-test.yml: templ generate, build, test.
test: templ-install
	@echo "Testing..."
	@templ generate -path .
	@go build -v ./...
	@go test ./... -v

# Lint the application
# Mirrors .github/workflows/go-lint.yml: templ generate, golangci-lint.
lint: templ-install golangci-install
	@echo "Linting..."
	@templ generate -path .
	@golangci-lint run ./...

# Clean the binary
clean:
	@echo "Cleaning..."
	@rm -f main

# Live Reload
air:
	@if command -v air > /dev/null; then \
            air; \
            echo "Watching...";\
        else \
            read -p "Go's 'air' is not installed on your machine. Do you want to install it? [Y/n] " choice; \
            if [ "$$choice" != "n" ] && [ "$$choice" != "N" ]; then \
                go install github.com/air-verse/air@latest; \
                air; \
                echo "Watching...";\
            else \
                echo "You chose not to install air. Exiting..."; \
                exit 1; \
            fi; \
        fi

watch:
	make -j 3 tailwind-watch templ-watch air

.PHONY: all build run test clean watch tailwind templ-install golangci-install lint ent



# live/templ:
# 	templ generate --watch --proxy="http://localhost:8081" --open-browser=false -v

# live/server:
# 	go run github.com/cosmtrek/air@v1.51.0 \
# 	--build.cmd "go build -o tmp/bin/main" --build.bin "tmp/bin/main" --build.delay "100" \
# 	--build.exclude_dir "node_modules" \
# 	--build.include_ext "go" \
# 	--misc.clean_on_exit true

# live/tailwind:
# 	npx tailwindcss -i ./input.css -o ./assets/styles.css --minify --watch

# live/esbuild:
# 	npx esbuild js/index.ts --bundle --outdir=assets/ --watch

# live/sync_assets:
# 	go run github.com/cosmtrek/air@v1.51.0 \
# 	--build.cmd "templ generate --notify-proxy" \
# 	--build.bin "true" \
# 	--build.delay "100" \
# 	--build.exclude_dir "" \
# 	--build.include_dir "assets" \
# 	--build.include_ext "js,css"

# live: 
# 	make -j5 live/templ live/server live/tailwind live/esbuild live/sync_assets