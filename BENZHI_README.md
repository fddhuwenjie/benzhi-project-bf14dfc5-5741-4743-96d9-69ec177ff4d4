# BENZHI_README

## 项目说明
- 项目：benzhi-project-bf14dfc5-5741-4743-96d9-69ec177ff4d4
- 项目用途：馆藏文物保存环境异常处置台提供环境异常登记、风险研判、分派执行、效果复核及可追溯关闭流程。
- Go 工具链：`golang:1.22`
- 前端工具链：原生 HTML、CSS 和 JavaScript，由 Go 服务直接提供

## 项目描述
- 项目名称：馆藏文物保存环境异常处置台
- 项目概述：面向中小型博物馆保管团队的单流程工作台，将展柜或库房环境异常从登记、风险研判、处置执行推进到复核关闭，并保留可追溯证据。
- 核心工作流：保管员登记环境异常并提交读数证据，系统校验后生成风险研判，负责人分派处置并确认措施方案，执行人记录措施及效果读数，复核人核验恢复情况后关闭事件；不合格时退回同一事件继续处置。
- 对外接口：由 Go 服务提供原生 HTML、CSS 和 JavaScript 的浏览器工作台，包含异常登记、待办队列、处置记录、复核操作和事件时间线，不引入 Node 构建链。

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...
cd /app && GOTOOLCHAIN=local go run ./cmd/server -addr=127.0.0.1:19081 -self-check
cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-project-bf14dfc5-5741-4743-96d9-69ec177ff4d4-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-project-bf14dfc5-5741-4743-96d9-69ec177ff4d4-arm64 linux/arm64
docker run -it benzhi-project-bf14dfc5-5741-4743-96d9-69ec177ff4d4-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/server -addr=127.0.0.1:19081 -self-check`
