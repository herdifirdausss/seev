.PHONY: help build run test clean migrate-up migrate-down docker-up docker-down

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the application
	@echo "Building..."
	@go build -o bin/kehidupanku main.go

run: ## Run the application
	@echo "Running..."
	@go run main.go

test: ## Run tests
	@echo "Running tests..."
	@go test -v ./...

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	@go test -cover -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf bin/
	@rm -f coverage.out coverage.html

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy

migrate-up: ## Run database migrations
	@echo "Running migrations..."
	@psql -U postgres -d kehidupanku -f migrations/001_create_auth_tables.sql

migrate-down: ## Rollback database migrations
	@echo "Rolling back migrations..."
	@psql -U postgres -d kehidupanku -c "DROP TABLE IF EXISTS refresh_tokens; DROP TABLE IF EXISTS users;"

docker-up: ## Start Docker containers
	@echo "Starting Docker containers..."
	@docker-compose up -d

docker-down: ## Stop Docker containers
	@echo "Stopping Docker containers..."
	@docker-compose down

lint: ## Run linter
	@echo "Running linter..."
	@golangci-lint run

format: ## Format code
	@echo "Formatting code..."
	@go fmt ./...

dev: ## Run in development mode with hot reload
	@echo "Starting development server..."
	@air
