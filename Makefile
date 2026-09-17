.PHONY: test vet docker compose-up compose-config

test:
	go test ./...

vet:
	go vet ./...

docker:
	docker build -t eval-display:local .

compose-up:
	docker compose up --build

compose-config:
	docker compose config
