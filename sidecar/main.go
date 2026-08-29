package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"supply-check-sdk/batch"
	"supply-check-sdk/protocol"
	"supply-check-sdk/providers"
)

func main() {
	response := handle(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func handle(reader io.Reader) protocol.Response {
	var request protocol.Request
	decoder := json.NewDecoder(io.LimitReader(reader, 2<<20))
	if err := decoder.Decode(&request); err != nil {
		return protocol.Response{Error: fmt.Sprintf("无法解析 SDK 请求: %v", err)}
	}
	if err := validate(request); err != nil {
		return protocol.Response{Error: err.Error()}
	}
	switch request.Action {
	case "models":
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		models, err := providers.ListModels(ctx, request)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return protocol.Response{Models: models}
	case "complete":
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		observation, err := providers.Complete(ctx, request)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return protocol.Response{Observation: observation}
	case "runAll":
		var progressMu sync.Mutex
		progressEncoder := json.NewEncoder(os.Stderr)
		report, err := batch.RunAll(context.Background(), request, func(progress batch.Progress) {
			progressMu.Lock()
			defer progressMu.Unlock()
			_ = progressEncoder.Encode(progress)
		})
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return protocol.Response{Report: report}
	default:
		return protocol.Response{Error: "不支持的 SDK 操作"}
	}
}

func validate(request protocol.Request) error {
	if strings.TrimSpace(request.Credentials.APIKey) == "" {
		return fmt.Errorf("请填写 API Key")
	}
	if strings.TrimSpace(request.Credentials.BaseURL) == "" {
		return fmt.Errorf("请填写 API Base URL")
	}
	if request.Action == "complete" {
		if strings.TrimSpace(request.Model) == "" {
			return fmt.Errorf("请选择模型")
		}
		if strings.TrimSpace(request.Prompt) == "" {
			return fmt.Errorf("探针 prompt 不能为空")
		}
	}
	if request.Action == "runAll" {
		if len(request.Models) == 0 {
			return fmt.Errorf("请先拉取模型列表")
		}
		if strings.TrimSpace(request.OutputPath) == "" {
			return fmt.Errorf("PDF 输出路径不能为空")
		}
	}
	return nil
}
