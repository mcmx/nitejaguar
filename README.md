# Project nitejaguar

Workflow automation written in Go

Created using go-blueprint

```bash
go-blueprint create --name nitejaguar --framework echo --driver sqlite --advanced --feature htmx --feature githubaction --feature websocket --feature tailwind
```

## Getting Started

These instructions will get you a copy of the project up and running on your local machine for development and testing purposes. See deployment for notes on how to deploy the project on a live system.

## MakeFile

Run build make command with tests
```bash
make all
```

Build the application
```bash
make build
```

Run the application
```bash
make run
```

Live reload the application:
```bash
make watch
```

Run the test suite:
```bash
make test
```

Clean up binary from the last build:
```bash
make clean
```
