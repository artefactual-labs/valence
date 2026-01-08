.PHONY: build dev gen

build:
	docker build -t valence-dev .

gen:
	go generate ./internal/atomembed

dev: build
	docker run --rm -p 127.0.0.1:14800:8080 valence-dev

shell:
	docker compose exec --user=root valence bash

pre-commit:
	uvx pre-commit run --all-files