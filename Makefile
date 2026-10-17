VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: web build desktop test dev tools release clean

web:            ## 构建前端并同步到 go:embed 目录
	npm --prefix web ci
	npm --prefix web run build
	node scripts/sync-web.mjs

build: web      ## 构建当前平台单文件
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/autocoder ./cmd/autocoder

desktop: web    ## 构建当前平台带菜单栏图标的桌面版（macOS 需要 CGO，在 Mac 上执行）
	CGO_ENABLED=$(if $(filter Darwin,$(shell uname)),1,0) go build -tags tray -trimpath -ldflags '$(LDFLAGS)' -o bin/autocoder-desktop ./cmd/autocoder

test:
	go vet ./...
	go vet -tags tray ./...
	go test ./...

tools:          ## 只做工具检测
	go run ./cmd/autocoder tools

release: web    ## 交叉编译全平台命令行版单文件（桌面版见 .github/workflows/release.yml）
	@for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
	  os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  echo "→ $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o dist/autocoder-$$os-$$arch$$ext ./cmd/autocoder || exit 1; \
	done

clean:
	rm -rf bin dist web/dist
	find internal/server/dist -mindepth 1 ! -name .keep -delete
