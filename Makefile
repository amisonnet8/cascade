# Cascade: .casソースをAMIVM-IR経由でGoにコンパイルする言語処理系

BINARY := cascade
PKG    := ./cmd/cascade
GO     := go

.PHONY: all build install test test-examples fmt vet tidy clean help

all: build ## デフォルトターゲット(ビルドのみ)

build: ## cascadeバイナリをビルドする
	$(GO) build -o $(BINARY) $(PKG)

install: ## cascadeバイナリをGOBIN($GOPATH/bin)へインストールする
	$(GO) install $(PKG)

test: ## go testで全パッケージのユニットテストを実行する
	$(GO) test ./...

test-examples: build ## examples/配下の全サンプル(.casファイル・パッケージディレクトリ双方)をamivm→go buildまで通し実行する(amivmがPATHにある前提)
	@set -e; \
	for src in examples/*; do \
		echo "== $$src =="; \
		./$(BINARY) run "$$src"; \
	done

fmt: ## *.goをgoimportsで整形する
	goimports -w .

vet: ## go vetで静的検査する
	$(GO) vet ./...

tidy: ## go.mod/go.sumを整理する
	$(GO) mod tidy

clean: ## ビルド成果物を削除する
	rm -f $(BINARY)

help: ## 使えるターゲット一覧を表示する
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'
