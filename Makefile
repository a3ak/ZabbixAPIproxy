.PHONY: build build-all test clean

# Собрать для текущей платформы
build:
	go build -ldflags="-X main.version=$(shell git describe --tags 2>/dev/null || echo 'dev')" -o zabbix-proxy ./cmd/app/main.go

# Собрать для Linux
build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-X main.version=$(shell git describe --tags 2>/dev/null || echo 'dev')" -o zabbix-proxy-linux ./cmd/app/main.go

# Запустить тесты
test:
	go test ./...

# Очистить
clean:
	rm -f zabbix-proxy zabbix-proxy-linux