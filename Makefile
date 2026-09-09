DOMAIN ?= localhost:8080
SCHEME ?= http

.PHONY: build run smoke

build:
	go build -o dist/proxy ./cmd/proxy

run:
	go run ./cmd/proxy -config config.yaml

# Quick health check of a running instance (local or deployed).
smoke:
	@set -e; \
	check() { code=$$(curl -s -o /dev/null -w "%{http_code}" "$$2"); \
	  if [ "$$code" = "$$1" ]; then echo "ok   $$2 -> $$code"; \
	  else echo "FAIL $$2 -> $$code (want $$1)"; exit 1; fi; }; \
	check 200 "$(SCHEME)://$(DOMAIN)/healthz"; \
	check 200 "$(SCHEME)://$(DOMAIN)/"; \
	check 200 "$(SCHEME)://yt.$(DOMAIN)/"; \
	check 403 "$(SCHEME)://yt.$(DOMAIN)/shorts/x"; \
	check 302 "$(SCHEME)://tt.$(DOMAIN)/"; \
	check 403 "$(SCHEME)://tt.$(DOMAIN)/foryou"; \
	check 403 "$(SCHEME)://ig.$(DOMAIN)/reels"; \
	echo "smoke passed"
