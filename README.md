# 货源体检桌面版

基于 Tauri v2 的本地桌面应用，用来审计一个 AI 上游渠道到底"兑没兑水"。

填入 API Base 和 API Key，用对应厂商的**官方 SDK** 拉取模型列表，然后对每个模型跑完整 22 项探针套件（约 63 次真实上游请求），最后生成 PDF 报告。

项目完全自包含：探针实现、评分器、PDF 渲染器都在 `sidecar/internal/` 下，除三家官方 SDK 外没有外部服务或仓库依赖。

## 架构

三层，各层只做一件事：

```
React + Fluent UI          收集参数、展示报告        src/
        │  invoke / event
Tauri Rust 核心            进程编排、路径与事件      src-tauri/src/
        │  stdin: 请求 JSON   stdout: 结果 JSON   stderr: 进度 JSON
Go SDK 侧车                官方 SDK 调用、评分、PDF   sidecar/
```

**为什么要 Go 侧车。** 三家厂商的官方 SDK 里，只有 Go 版本能做到探针需要的原生协议保真（流式帧、缓存遥测、工具 Schema 这些细节不能被二次封装吃掉）。侧车是个一次性进程：读一份 stdin JSON，干完活，把结果 JSON 写到 stdout 就退出。

**三条流不混用。** `stdout` 只放最终结果，`stderr` 只放逐行的进度 JSON，Rust 侧解析后重新广播成 Tauri 事件 `healthcheck-progress`；stderr 上无法解析成进度的行会被收集为诊断信息，在侧车非零退出时回传。

**API Key 只走 stdin。** 不进命令行参数（避免出现在进程列表）、不进报告、不进 localStorage。前端只把 Base URL 持久化到 `localStorage` 的 `supply-check-bases-v1`。

### 四个协议

`openai`、`openai-responses`、`anthropic`、`google`。前两个都指向 OpenAI，但走不同端点：`/v1/chat/completions` 和 `/v1/responses`。它们并列而非替代 —— 很多第三方中转站只兑了 chat completions，分成两个协议才能对比同一渠道在两个端点下的表现，而不是悄悄回退掉。两者共用 `/v1/models` 拉列表，探针侧也共享 tokenizer 与 prompt 缓存语义（见 `runner.go` 的 `isOpenAIFamily`）。

### Tauri 命令

| 命令 | 作用 |
| --- | --- |
| `list_models` | 用官方 SDK 拉模型列表，过滤掉不能生成内容的模型 |
| `run_all_healthchecks` | 批量体检 + 生成 PDF，返回 `BatchReport` |
| `open_pdf` | 在系统文件管理器中打开报告（目前仅 Windows 实现） |
| `run_healthcheck` | **遗留路径**，见下 |

`run_healthcheck` 对应 `src-tauri/src/engine.rs`，是早期用 Rust 重写的单模型 7 请求精简版探针逻辑。当前前端不再调用它，实际生效的是 `sidecar/batch/runner.go`。改探针逻辑时**认准 Go 那份**，别改到 `engine.rs`。

## 目录结构

```
src/                前端。App.tsx 是唯一的页面，ReportView.tsx 渲染报告
src-tauri/src/      sdk_bridge.rs 负责起进程与解析流，models.rs 是与前端的契约
sidecar/
  main.go           stdin → action 分派（models / complete / runAll）
  protocol/         三层共用的 JSON 契约
  providers/        openai.go / openai_responses.go / anthropic.go / google.go
  batch/runner.go   22 项探针编排、PDF 落盘 —— 编排逻辑都在这里
  internal/         见下
  cmd/pdf-sample/   用假数据生成一份 PDF，用于检查排版
scripts/build-sidecar.ts   按当前平台交叉编译侧车到 src-tauri/binaries/
```

`sidecar/internal/` 是体检的判定内核，改这里等于改结论：

| 包 | 职责 |
| --- | --- |
| `model/` | 探针种类、状态、判定枚举与 `ProbeSpec`/`ProbeResult` 契约 |
| `pricetest/` | 22 项探针的判定实现 + 信任分评分器 |
| `healthcheck/` | PDF 渲染器（手写 PDF 1.7，无第三方 PDF 库） |
| `i18n/` | 报告文案，7 种语言各 86 条，用 `go:embed` 打进二进制 |
| `service/` | 本地 Token 重算（tiktoken + 估算器），P1 探针的比对基准 |
| `common/` | 证据脱敏，防止上游报文里的密钥/域名进报告 |

`internal/pricetest` 与 `internal/healthcheck` 都带有完整单测（`go test ./internal/...`），这些测试是判定逻辑的行为基线，改探针时应当先看它们是否还成立。

## 环境要求

Bun、Go 1.25+、Rust、以及 Tauri v2 的平台构建依赖（Windows 上需要 MSVC 工具链和 WebView2）。

没有其他前提 —— 侧车不依赖任何外部仓库或服务，`git clone` 后直接就能构建。

## 开发

```powershell
bun install
bun run tauri dev
```

`tauri` 脚本会先编译当前平台的 Go 侧车，再启动桌面应用。前端跑在 `127.0.0.1:1420`（`strictPort`，端口被占就直接报错而不是换端口）。

只调前端界面、不需要真实体检时，可以单跑 `bun run dev`，但所有 `invoke` 都会失败。

## 构建

```powershell
bun run build                      # 侧车 + 前端 + 安装包
bun run tauri build --no-bundle    # 只出可执行文件，不打包安装程序
```

侧车产物带目标三元组后缀（`supply-check-sdk-x86_64-pc-windows-msvc.exe`），这是 Tauri `externalBin` 的约定；Tauri 打包时会去掉后缀。`sdk_bridge.rs` 里 `resolve_sidecar` 会依次在资源目录、可执行文件同级目录、`src-tauri/binaries/` 里找两种命名，所以 dev 和打包后都能定位到。

## 测试

```powershell
bun run frontend:build             # tsc 类型检查 + vite build
cd sidecar; go test ./...
cd ..; bun run test:rust
```

Go 侧的判定内核（`internal/pricetest`、`internal/healthcheck`）有完整单测覆盖，另有两个契约测试盯住容易被改坏的地方：`TestCompleteSuiteContract` 锁死"22 项定义 / 63 次请求"这两个数字（前端 `REQUESTS_PER_MODEL` 常量与之对应，改动时两边都要同步），`TestWritePDFUsesOriginalBatchRenderer` 验证批量报告确实产出了合法 PDF 且包含汇总页与模型明细页。

检查 PDF 排版：

```powershell
cd sidecar
go run ./cmd/pdf-sample ../tmp/pdfs/sample.pdf
```

## 体检套件

拉到模型列表后默认全选。每个模型跑 22 项结果定义、约 63 次逻辑请求：

- **真实性（6）** Token 计数、输出长度、模型身份、动态金标题、延迟吞吐、成本锚点
- **缓存（4）** 缓存记账、缓存新鲜度、Provider Cache-Control、长上下文缓存率
- **协议（7）** 来源自述、协议契约、流式完整性、用量对账、取消契约、工具 Schema、限流契约
- **安全与纯度（5）** 提示词泄露、指令策略、工具替换、上下文完整性、渠道纯度

几个执行细节：

- 每个请求最多重试 3 次（退避 100/200ms），单次尝试超时 300 秒。失败的探针记为 `error`，**不扣信任分**，但会让整体判定降级为"未测出"
- 关键身份类探针失败会直接判定为**兑水**
- 成本锚点恒为 `SKIP` —— 纯 API Base/Key 模式下没有可比对的客户价格账本，这一项明确标注而不是静默删掉
- 批量分数取各模型的算术平均；批量判定取最差的那个模型（兑水 > 可疑 > 未测出 > 清白）
- 金标题、缓存标记、上下文标记都按运行时随机种子生成，避免上游针对固定题目作弊

## 报告

体检结束后由 `internal/healthcheck.BuildHealthCheckJobPDF` 渲染 PDF，默认写到系统下载目录（拿不到时退回应用数据目录）。渲染器手写 PDF 1.7，用内置 CID 字体排 CJK，不带任何字体文件或第三方 PDF 依赖。前端界面另有可交互的逐探针证据视图。

报告内容与文件名都跟界面语言走：中文下是 `货源体检-OpenAI-20260829-153012.pdf`，英文下是 `supply-check-OpenAI-20260829-153012.pdf`。语言标签从前端一路透传到 `writePDF`，`pdfLang` 会把未知值收敛回 `zh-CN`。渲染器本身还支持 zh-TW / fr / ru / ja / vi，前端加语言时这一侧不用改。

## 注意成本

并发是**请求级**的，不是模型级：所有选中的模型同时铺开跑，闸门限制的是同时在飞的上游请求总数，与选了几个模型无关。闸门在 `runner.execute` 里，那是所有请求的唯一出口。默认 2，界面滑条可调到 16。

全选大量模型会产生 `模型数 × 约 63` 次真实上游请求和相应费用 —— 单个模型的缓存率探针就要打 33 次、每次带 16000 字符上下文。先用一两个模型试跑，再决定要不要全量。

并发调高会让峰值请求量成倍上升。这时触发的限流可能是并发压出来的，而不是渠道本身的行为，限流契约那一项的结论会因此失真。
