# 货源体检桌面版

基于 Tauri v2 的本地桌面应用，用来审计一个 AI 上游渠道到底"兑没兑水"。

填入 API Base 和 API Key，用对应厂商的**官方 SDK** 拉取模型列表，然后对每个模型跑完整 22 项探针套件（约 60 次真实上游请求），最后生成 PDF 报告。

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

判定逻辑只有一份，就在 `sidecar/batch/runner.go` 与 `sidecar/internal/pricetest/` 下。Rust 侧只做进程编排与流解析，不做任何探针判定。

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

交叉编译侧车（CI 用，本地一般不需要）：

```powershell
bun scripts/build-sidecar.ts --target=aarch64-apple-darwin
```

侧车是纯 Go 且 `CGO_ENABLED=0`，六个 target 在任意宿主上都能编出来。Tauri 本体不行，必须在目标平台原生构建。

## CI 与发布

`.github/workflows/ci.yml` 每次 push 跑三件事：侧车的 `go vet` + `go test -race` + 六平台交叉编译、前端的 `tsc` + `vite build`、以及三平台的 `cargo test`。`-race` 不是可选项 —— 并发改成请求级之后所有模型 goroutine 同时写 `reports[]` 并共享计数器。

`.github/workflows/release.yml` 由 `v*` tag 触发（也可手动 dispatch），出六份产物并建草稿 release：

| 平台 | 产物 |
| --- | --- |
| Windows x64 / arm64 | `.msi`（en-US 与 zh-CN 各一份） |
| macOS x64 / arm64 | `.dmg` |
| Linux x64 | `.deb` + `.AppImage` |
| Linux arm64 | `.deb` |

只有 Linux arm64 走交叉编译，需要 arm64 版 WebKitGTK/GTK。那里要把主 apt 源限定成 `[arch=amd64]` 再另加 ports 源，否则 `apt update` 会因为 amd64 源不提供 arm64 包而大量 404。bundle 类型在工作流里按平台显式指定，没有沿用 config 里的 `"targets": "all"` —— 那个在 Linux 上会连 rpm 一起打，在 Ubuntu runner 上常因缺依赖失败；Linux arm64 只出 deb，因为 AppImage 的打包工具跑在 amd64 上，交叉场景不可靠。

产物**未做代码签名**，macOS 和 Windows 首次打开都需要手动允许。

## 测试

```powershell
bun run frontend:build             # tsc 类型检查 + vite build
cd sidecar; go test ./...
cd ..; bun run test:rust
```

Go 侧的判定内核（`internal/pricetest`、`internal/healthcheck`）有完整单测覆盖。几组关键测试：

- `internal/pricetest/adversarial_score_test.go` —— **对抗性评分套件**。假设中转站读过本仓库源码，验证"拒绝作答"永远不会比"如实作答"拿到更好的判定。这是整个工具可信度的地基。
- `batch/request_contract_test.go` —— 用计数桩驱动整套探针，**实测** `execute()` 调用次数与 `RequestsPerModel` 一致（前端 `REQUESTS_PER_MODEL` 必须同步）。旧版契约测试只是在断言常量自己的算式，因此没能拦住前端漂移到 63。
- `internal/service/token_accounting_test.go` —— 验证诚实渠道不会因为消息封装开销被误判。
- `internal/common/mask_test.go` —— 证据脱敏。报告是用户会公开分享的材料，密钥泄漏等同于发布。
- `TestWritePDFUsesOriginalBatchRenderer` —— 批量报告确实产出合法 PDF 且包含汇总页与模型明细页。

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

- 每个请求最多重试 3 次（退避 100/200ms），单次尝试超时 300 秒。失败的探针记为 `error`，**不扣信任分** —— 上游不稳定不等于兑水
- **证据门槛**：`error` 不扣分这件事本身可以被利用 —— 一个读过源码的中转站只要把身份、自述、金标、Token 这几项打成超时，剩下的 PASS 就能把分数留在 100。所以清白/可疑判定额外要求两组探针各自至少有一项拿到真实信号：身份组（`identity` / `self_report`）与真实性组（`token_count` / `length` / `golden`）。任一组全军覆没 → 判定强制为"未测出"，绝不是"清白"。报告里的 `criticalErrorRate` 与 `insufficientReason` 就是这个结论的依据
- 反过来，**已经观测到的铁证不会被沉默洗掉**：身份不符或缓存重放一旦被抓到，即使其余探针全部报错，判定仍然是兑水
- 关键身份类探针失败会直接判定为**兑水**
- 成本锚点恒为 `SKIP` —— 纯 API Base/Key 模式下没有可比对的客户价格账本，这一项明确标注而不是静默删掉
- 批量分数取各模型的算术平均；批量判定取最差的那个模型（兑水 > 可疑 > 未测出 > 清白）
- 金标题、缓存标记、上下文标记都按运行时随机种子生成，避免上游针对固定题目作弊

## 报告

体检结束后由 `internal/healthcheck.BuildHealthCheckJobPDF` 渲染 PDF，默认写到系统下载目录（拿不到时退回应用数据目录）。渲染器手写 PDF 1.7，用内置 CID 字体排 CJK，不带任何字体文件或第三方 PDF 依赖。前端界面另有可交互的逐探针证据视图。

报告内容与文件名都跟界面语言走：中文下是 `货源体检-OpenAI-20260829-153012.pdf`，英文下是 `supply-check-OpenAI-20260829-153012.pdf`。语言标签从前端一路透传到 `writePDF`，`pdfLang` 会把未知值收敛回 `zh-CN`。渲染器本身还支持 zh-TW / fr / ru / ja / vi，前端加语言时这一侧不用改。

## 注意成本

并发是**请求级**的，不是模型级：所有选中的模型同时铺开跑，闸门限制的是同时在飞的上游请求总数，与选了几个模型无关。闸门在 `runner.execute` 里，那是所有请求的唯一出口。默认 2，界面滑条可调到 16。

全选大量模型会产生 `模型数 × 约 60` 次真实上游请求和相应费用。**缓存率探针是主要成本来源**：它按 10 档上下文长度（16,000 到 250,000 字符均匀分布）各跑 1 冷 + 2 温三轮，单个模型合计约 **400 万字符 ≈ 100 万 input token**。先用一两个模型试跑，再决定要不要全量。

超长上下文那几档会超出部分模型的窗口上限，这类请求会被上游拒绝并记为 `error`（不扣信任分，但会让缓存率一项判为证据不完整）。

另外注意重试：单个探针失败会重试最多 3 次，**每次尝试都是计费请求**，所以实际账单可能高于「模型数 × 60」。

并发调高会让峰值请求量成倍上升。这时触发的限流可能是并发压出来的，而不是渠道本身的行为，限流契约那一项的结论会因此失真。
