service ?=
stage ?= dev
region ?= us-east-1

.PHONY: help install dev build validate deploy package clean remove check-service

help:
	@echo ""
	@echo "Comandos disponibles:"
	@echo ""
	@echo "  make install"
	@echo "  make dev service=services/api-auth stage=local region=us-east-1"
	@echo "  make build service=services/api-auth"
	@echo "  make validate service=services/api-auth stage=dev region=us-east-1"
	@echo "  make deploy service=services/api-auth stage=dev region=us-east-1"
	@echo "  make deploy-quick service=services/api-auth stage=dev region=us-east-1"
	@echo "  make package service=services/api-auth stage=dev region=us-east-1"
	@echo "  make clean service=services/api-auth"
	@echo "  make remove service=services/api-auth stage=dev region=us-east-1"
	@echo ""

install:
	npm install

check-service:
	@if [ -z "$(service)" ]; then \
		echo "Error: debes indicar service=services/api-auth"; \
		exit 1; \
	fi
	@if [ ! -d "$(service)" ]; then \
		echo "Error: el servicio no existe: $(service)"; \
		exit 1; \
	fi
	@if [ ! -f "$(service)/serverless.ts" ]; then \
		echo "Error: no existe $(service)/serverless.ts"; \
		exit 1; \
	fi
	@if [ ! -f "$(service)/service.config.json" ]; then \
		echo "Error: no existe $(service)/service.config.json"; \
		exit 1; \
	fi

dev: check-service
	npx tsx scripts/dev-service.ts --service $(service) --stage $(stage) --region $(region)

build: check-service
	npx tsx scripts/build-service.ts --service $(service)

validate: check-service
	npx tsx scripts/validate-service.ts --service $(service) --stage $(stage) --region $(region)

deploy: check-service
	npx tsx scripts/deploy-service.ts --service $(service) --stage $(stage) --region $(region)

deploy-quick: check-service
	npx tsx scripts/deploy-service.ts --service $(service) --stage $(stage) --region $(region) --skip-build --skip-validate

package: check-service build validate
	cd $(service) && NODE_OPTIONS='--disable-warning=DEP0169' npx serverless package --stage $(stage) --region $(region)

clean: check-service
	rm -rf $(service)/.serverless
	rm -rf $(service)/.serverless-artifacts

remove: check-service
	cd $(service) && NODE_OPTIONS='--disable-warning=DEP0169' npx serverless remove --stage $(stage) --region $(region)
