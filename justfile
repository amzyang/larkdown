version := "v2-" + `git rev-parse --short HEAD`

# 构建 CLI 工具 (larkdown)
build:
    go build -ldflags="-X main.version={{version}}" -o ./larkdown cmd/*.go

# 运行所有测试
test:
    go test ./...

# 代码格式化
format:
    gofmt -l -w .

# 删除构建产物
clean:
    rm -f ./larkdown
