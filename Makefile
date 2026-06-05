export DOCKER_CLI_EXPERIMENTAL=enabled

PLATFORM?="linux/amd64,linux/arm/v7,linux/arm64"

TAG?=dev
SERVER?=ttl.sh
OWNER?=openfaas
NAME=gateway

.PHONY: dist-local
dist-local:
	CGO_ENABLED=0 go build -buildvcs=false -o bin/faasd-gateway

.PHONY: install
install: dist-local
	# Install faasd-gateway binary
	echo "Installing new faasd-gateway binary..."
	sudo rm -f /usr/local/bin/faasd-gateway
	sudo cp bin/faasd-gateway /usr/local/bin/faasd-gateway
	sudo chmod +x /usr/local/bin/faasd-gateway

.PHONY: buildx-local
buildx-local:
	@echo $(SERVER)/$(OWNER)/$(NAME):$(TAG) \
	&& docker buildx create --use --name=multiarch --node multiarch \
	&& docker buildx build \
		--progress=plain \
		--platform linux/amd64 \
		--output "type=docker,push=false" \
		--tag $(SERVER)/$(OWNER)/$(NAME):$(TAG) .

.PHONY: buildx-push
buildx-push:
	@echo $(SERVER)/$(OWNER)/$(NAME):$(TAG) \
	&& docker buildx create --use --name=multiarch --node multiarch \
	&& docker buildx build \
		--progress=plain \
		--platform linux/amd64 \
		--output "type=image,push=true" \
		--tag $(SERVER)/$(OWNER)/$(NAME):$(TAG) .

.PHONY: buildx-push-all
buildx-push-all:
	@echo $(SERVER)/$(OWNER)/$(NAME):$(TAG) \
	&& docker buildx create --use --name=multiarch --node multiarch \
	&& docker buildx build \
		--progress=plain \
		--platform $(PLATFORM) \
		--output "type=image,push=true" \
		--tag $(SERVER)/$(OWNER)/$(NAME):$(TAG) .

# generate Go models from the OpenAPI spec using https://github.com/contiamo/openapi-generator-go
generate:
	rm models/model_*.go || true
	openapi-generator-go generate models -s api-docs/spec.openapi.yml -o models --package-name models

.PHONY: test
test:
	go test -v ./...
