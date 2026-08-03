# vmount

将 S3 兼容存储挂载为 Windows 驱动器（默认 Z:）的简单工具。适合需要把远端对象存储以文件系统方式访问的场景，支持本地缓存与分片上传策略，基于 Go 实现并使用 WinFsp + cgofuse 在 Windows 上提供驱动器挂载。

## 主要特性
- 将 S3（或 S3 兼容服务）挂载为本地驱动器（Windows）
- 本地读缓存（可配置大小）
- 列表缓存（TTL 可配置）
- 支持大文件的分片上传（可配置阈值与分片大小）
- 在 SIGINT / SIGTERM 时自动卸载

## 先决条件
- Go 工具链（用于构建）
- Windows: 安装 WinFsp（用于用户态文件系统支持）并以管理员权限运行可挂载驱动器
- 可访问的 S3 兼容存储（Endpoint、Bucket、AccessKey、SecretKey）

## 快速开始（从源码）
1. 克隆仓库
   ```bash
   git clone https://github.com/Yuan720/vmount.git
   cd vmount
   ```
2. 构建可执行文件
   ```bash
   go build -o vmount ./cmd/vmount
   ```
3. 复制并编辑配置
   ```bash
   cp config.example.json vmount.json
   # 编辑 vmount.json，填入 Endpoint/Bucket/AccessKey/SecretKey 等字段
   ```
4. 以管理员身份运行（Windows）并指定配置文件（默认配置文件名为 vmount.json）：
   ```bash
   ./vmount -config vmount.json
   ```
   或（在 Windows PowerShell 下）
   ```powershell
   .\\vmount.exe -config vmount.json
   ```
   程序启动后会打印类似：
   ```
   mounting Z: -> s3://<bucket>/<prefix>
   ```
   按 Ctrl+C 或结束进程会触发卸载并退出。

## 配置（字段说明）
程序通过 JSON 配置文件加载（默认名 vmount.json，或通过 -config 指定）。可用字段：

- endpoint: S3 API 地址（必填）
- bucket: 桶名（必填）
- prefix: 对象前缀（可选）
- access_key: 访问密钥（必填）
- secret_key: 密钥（必填）
- mount: 挂载点（Windows 驱动器，如 "Z:"；默认 "Z:")
- cache_dir: 本地缓存目录（默认 "cache")
- read_cache_mb: 读缓存大小（MB，默认 512)
- list_ttl_sec: 列表缓存有效期（秒，默认 30)
- multipart_threshold: 使用分片上传的阈值（字节，默认 100 MB)
- chunk_size: 分片大小（字节，默认 8 MB)
- use_tls: 是否使用 TLS（布尔）

示例 vmount.json：
```json
{
  "endpoint": "https://s3.example.com",
  "bucket": "my-bucket",
  "prefix": "path/prefix",
  "access_key": "AKIA...",
  "secret_key": "SECRET...",
  "mount": "Z:",
  "cache_dir": "cache",
  "read_cache_mb": 512,
  "list_ttl_sec": 30,
  "multipart_threshold": 104857600,
  "chunk_size": 8388608,
  "use_tls": true
}
```

## 架构与实现要点
- cmd/vmount/main.go：程序入口，读取配置，初始化 S3 客户端与文件系统实例，使用 cgofuse + WinFsp 提供挂载并响应系统信号进行卸载。
- internal/config：配置结构与解析（包含默认值）。
- internal/s3client：与 S3 兼容服务交互的客户端（支持指定 Endpoint、凭证与超时）。
- internal/fs：实现文件系统逻辑（目录/文件的读取、写入、缓存与分片上传处理）。
- 本地缓存目录用于减少频繁请求，提高性能；列表结果有短期缓存以降低列举请求压力。

## 开发与测试
- 运行全量测试（如果存在测试文件）：
  ```bash
  go test ./...
  ```
- 本地调试可以通过更改配置中的 mount 为临时目录（在类 Unix 环境调试）或在 Windows 上使用测试驱动方式。

## 常见问题与排查
- 挂载失败：
  - 确认 WinFsp 已正确安装并重启系统后再次尝试。
  - 以管理员权限运行可执行文件（Windows）。
  - 检查 vmount.json 中 Endpoint / Bucket / AccessKey / SecretKey 是否正确。
- 性能问题：
  - 增大 read_cache_mb 或调整 chunk_size 可改善大文件读写性能。
  - 减少 list_ttl_sec 会导致更多列举请求，增加延迟与请求次数。
- 卸载失败：
  - 使用任务管理器结束进程后，若驱动器仍显示占用，可在 Windows 中使用 WinFsp 提供的工具或重新启动以清理。

## 文件与目录（仓库概要）
- cmd/vmount/ : 主程序入口
- internal/config/ : 配置加载与默认值
- internal/s3client/ : S3 兼容客户端实现
- internal/fs/ : FUSE 文件系统实现
- test/ : 测试相关（如有）

## 贡献
欢迎提交 Issue 或 Pull Request。建议在 PR 中说明复现步骤与测试方式。

## 许可
This project is licensed under the MIT License — see the [LICENSE](./LICENSE) file for details.
