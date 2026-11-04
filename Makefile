.PHONY: all build dev clean test test-race test-fuzz docker-build up down demo eval help

PLUGIN_NAME = staledetector
OUTPUT_DIR  = build
OUTPUT      = $(OUTPUT_DIR)/$(PLUGIN_NAME).so

DOCKER_IMAGE = golang:1.27.3-alpine3.24@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd

all: build

help: ## Display available targets
	@echo "Available make targets for stale-answer-detector:"
	@echo "  build         Build plugin .so using native toolchain (Linux/macOS)"
	@echo "  docker-build  Build plugin and dynamic gateway inside pinned Docker container"
	@echo "  up            Start dynamic Bifrost gateway with stale-answer-detector mounted"
	@echo "  down          Stop containers"
	@echo "  test          Run unit tests for pure-Go freshness library"
	@echo "  test-race     Run race condition detection tests"
	@echo "  test-fuzz     Run Tier 1 parser fuzz tests"
	@echo "  eval          Execute 60-case benchmark evaluation"
	@echo "  demo          Send sample request to check verdict and freshness score"
	@echo "  clean         Clean up build artifacts"

clean: ## Clean build artifacts
	@rm -rf $(OUTPUT_DIR) tmp/

build: clean ## Build the plugin .so (requires Linux/macOS host with CGO)
	@mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=1 go build -buildmode=plugin -ldflags="-w -s" -trimpath -o $(OUTPUT) staledetector.go
	@echo "✓ Plugin built: $(OUTPUT)"

docker-build: ## Build dynamic gateway and plugin in pinned Alpine Docker image
	@echo "Building dynamic gateway and plugin in $(DOCKER_IMAGE)..."
	docker compose -f docker/docker-compose.yml build

up: ## Start Bifrost gateway container with plugin mounted
	docker compose -f docker/docker-compose.yml up -d
	@echo "Gateway started at http://localhost:8080"

down: ## Stop Bifrost gateway container
	docker compose -f docker/docker-compose.yml down

test: ## Run pure-Go freshness library unit tests
	go test -v -cover ./freshness/...

test-race: ## Run race detection tests
	go test -v -race ./freshness/...

test-fuzz: ## Run fuzz tests on Tier 1 candidate detector
	go test -run=Fuzz -fuzz=FuzzTier1Detect -fuzztime=10s ./freshness/...

eval: ## Run the 60-case evaluation benchmark
	go run ./eval/runner.go -dataset ./eval/dataset.jsonl -mode replay

demo: ## Send sample request to gateway to observe freshness headers and verdict
	@echo "Sending test request to Bifrost gateway..."
	@curl -s -i http://localhost:8080/v1/chat/completions \
		-H "Content-Type: application/json" \
		-H "Authorization: Bearer mock-key" \
		-d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "What is the price of GPT-4o and what is the latest Python version?"}]}'

