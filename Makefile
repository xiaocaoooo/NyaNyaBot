GO ?= go
BIN_DIR ?= bin
PLUGIN_DIR ?= plugins

# 主程序名称
MAIN := nyanyabot

# 自动发现 cmd/nyanyabot-plugin-* 目录
PLUGIN_DIRS := $(notdir $(wildcard cmd/nyanyabot-plugin-*))
PLUGIN_BINS := $(addprefix $(PLUGIN_DIR)/,$(PLUGIN_DIRS))
PLUGIN_TARGETS := $(addprefix build-,$(PLUGIN_DIRS))
MAIN_BINARY := $(BIN_DIR)/$(MAIN)

.PHONY: all build build-main build-plugins clean test test-plugins test-main fmt tidy tidy-test vet help
.PHONY: $(PLUGIN_TARGETS)

all: build

# 编译主程序与全部插件
build: $(MAIN_BINARY) $(PLUGIN_BINS)

# 编译主程序
build-main: $(MAIN_BINARY)
$(MAIN_BINARY): | $(BIN_DIR)
	$(GO) build -o $@ ./cmd/$(MAIN)

# 一键编译所有 plugin
build-plugins: $(PLUGIN_BINS)

# 逐个编译插件（生成如 make build-nyanyabot-plugin-echo）
$(PLUGIN_TARGETS): build-%: $(PLUGIN_DIR)/%

# 插件二进制编译规则
$(PLUGIN_DIR)/%: | $(PLUGIN_DIR)
	$(GO) build -o $@ ./cmd/$*

$(BIN_DIR):
	mkdir -p $@

$(PLUGIN_DIR):
	mkdir -p $@

# 运行测试（主程序/插件全部 go package）
test: test-main test-plugins

test-main:
	$(GO) test ./cmd/$(MAIN)

test-plugins:
	$(GO) test ./cmd/nyanyabot-plugin-*

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

vet:
	$(GO) vet ./...

clean:
	rm -rf $(BIN_DIR) $(PLUGIN_DIR)

help:
	@echo "可用目标:"
	@echo "  make            # 编译主程序和全部插件"
	@echo "  make build-main # 仅编译主程序"
	@echo "  make build-plugins # 编译全部插件"
	@echo "  make build-<plugin_name> # 编译单个插件，例如: make build-nyanyabot-plugin-echo"
	@echo "  make test       # 运行主程序和插件测试"
	@echo "  make clean      # 清理构建产物"
