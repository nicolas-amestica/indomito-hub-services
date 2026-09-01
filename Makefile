service ?=
stage ?= dev
region ?= us-east-1

.PHONY: help install dev build validate deploy package clean remove check-service

help:
	@echo ""
	@echo "Comandos disponibles (reemplaza <nombre> por el servicio, ej: api-viajes)"
	@echo ""
	@echo "  make install"
	@echo "  make dev service=services/<nombre> stage=local region=us-east-1"
	@echo "  make build service=services/<nombre>"
	@echo "  make validate service=services/<nombre> stage=dev region=us-east-1"
	@echo "  make deploy service=services/<nombre> stage=dev region=us-east-1"
	@echo "  make deploy-quick service=services/<nombre> stage=dev region=us-east-1"
	@echo "  make package service=services/<nombre> stage=dev region=us-east-1"
	@echo "  make clean service=services/<nombre>"
	@echo "  make remove service=services/<nombre> stage=dev region=us-east-1"
	@echo ""
	@echo "  Ver README.md para crear un servicio nuevo desde cero."
	@echo ""

install:
	npm install

check-service:
	@if [ -z "$(service)" ]; then \
		echo "Error: debes indicar service=services/<nombre>"; \
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
	npx tsx scripts/deploy-service.ts --service $(service) --stage $(stage) --region $(region) --remove
