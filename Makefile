.PHONY: build run test docker-build docker-push deploy clean

# Build the binary
build:
	go build -o bin/media-processor ./cmd/processor

# Run locally
run:
	go run ./cmd/processor

# Run tests
test:
	go test -v ./...

# Build Docker image
docker-build:
	docker build -t lowreyarchives/media-processor:latest .

# Push Docker image (customize registry as needed)
docker-push: docker-build
	docker push lowreyarchives/media-processor:latest

# Deploy to k3s
deploy:
	kubectl apply -k deployments/k8s/

# Delete deployment
undeploy:
	kubectl delete -k deployments/k8s/

# View logs
logs:
	kubectl logs -n lowreyarchives -l app.kubernetes.io/name=media-processor -f

# Get pod status
status:
	kubectl get pods -n lowreyarchives -l app.kubernetes.io/name=media-processor

# Clean build artifacts
clean:
	rm -rf bin/
	go clean

# Download dependencies
deps:
	go mod download
	go mod tidy

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run ./...
