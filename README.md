# Mimo2API

将小米 Mimo AI 转换为 OpenAI 兼容 API，支持深度思考

## 安装

### 方式一：下载预编译二进制

从 [Releases](https://github.com/leookun/mimo2api/releases) 页面下载对应系统的二进制文件：

**Linux:**
```bash
curl -LO https://github.com/leookun/mimo2api/releases/latest/download/mimo2api-linux-amd64
chmod +x mimo2api-linux-amd64
./mimo2api-linux-amd64
```

**macOS (Intel):**
```bash
curl -LO https://github.com/leookun/mimo2api/releases/latest/download/mimo2api-darwin-amd64
chmod +x mimo2api-darwin-amd64
sudo xattr -cr mimo2api-darwin-amd64  # 解除安全限制
./mimo2api-darwin-amd64
```

**macOS (Apple Silicon):**
```bash
curl -LO https://github.com/leookun/mimo2api/releases/latest/download/mimo2api-darwin-arm64
chmod +x mimo2api-darwin-arm64
sudo xattr -cr mimo2api-darwin-arm64  # 解除安全限制
./mimo2api-darwin-arm64
```

**Windows:**

下载 `mimo2api-windows-amd64.exe`，双击运行或在命令行执行。

### 方式二：从源码编译

```bash
git clone https://github.com/leookun/mimo2api.git
cd mimo2api
go build -o mimo2api .
./mimo2api
```

## 运行

```bash
# 默认端口 8080
./mimo2api

# 指定端口
PORT=3000 ./mimo2api
```

## 配置

访问 `http://localhost:8080` 进入管理界面配置账号。

## API 使用

### 端点

```
POST /v1/chat/completions
```

### 请求示例

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-default" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2-flash-studio",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

### 启用深度思考

添加 `reasoning_effort` 参数：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-default" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-v2-flash-studio",
    "messages": [{"role": "user", "content": "解释量子纠缠"}],
    "stream": true,
    "reasoning_effort": "medium"
  }'
```

`reasoning_effort` 可选值：`low` / `medium` / `high`（任意非空值均可启用）

### 响应格式

**流式响应** - 思考内容在 `delta.reasoning` 字段：

```json
{"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning":"思考内容..."}}]}
{"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"回复内容..."}}]}
```

**非流式响应** - 思考内容以 `<think>` 标签包裹：

```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "<think>思考内容</think>\n回复内容"
    },
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 10, "completion_tokens": 50, "total_tokens": 60}
}
```

## 获取 Mimo 账号凭证

1. 登录 https://aistudio.xiaomimimo.com
2. 打开浏览器开发者工具 → Network(网络)
3. 发送一条消息，找到 `chat` 请求
4. 右键 → Copy as cURL
5. 在管理界面粘贴会自动解析
