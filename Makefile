.PHONY: test vet docker compose-up compose-minio compose-config

test:
	go test ./...

vet:
	go vet ./...

docker:
	docker build -t eval-display:local .

compose-up:
	docker compose up --build

compose-minio:
	EVAL_DISPLAY_S3_BACKEND=aws \
	EVAL_DISPLAY_S3_ENDPOINT=http://minio:9000 \
	EVAL_DISPLAY_S3_BUCKET=results \
	EVAL_DISPLAY_S3_ACCESS_KEY=minio \
	EVAL_DISPLAY_S3_SECRET_KEY=minio-dev-secret \
	EVAL_DISPLAY_S3_PATH_STYLE=true \
	docker compose --profile minio up --build

compose-config:
	docker compose config
